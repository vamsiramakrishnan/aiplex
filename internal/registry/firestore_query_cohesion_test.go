package registry

import (
	"context"
	"fmt"
	"net"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	pb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// This fixture accepts the actual SDK Commit wire documents and evaluates the
// actual SDK RunQuery filters/order/limit against those same field paths. It
// deliberately does not reinterpret json tags or normalize field-name casing.
type queryWireServer struct {
	*quotaWireServer
	queries                  []*pb.StructuredQuery
	beforeTransactionalQuery func(*queryWireServer)
}

func (s *queryWireServer) Commit(ctx context.Context, request *pb.CommitRequest) (*pb.CommitResponse, error) {
	if len(request.Transaction) > 0 {
		return s.quotaWireServer.Commit(ctx, request)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	response := &pb.CommitResponse{CommitTime: timestamppb.Now()}
	for _, write := range request.Writes {
		if doc := write.GetUpdate(); doc != nil {
			stored := proto.Clone(doc).(*pb.Document)
			stored.CreateTime = timestamppb.Now()
			stored.UpdateTime = timestamppb.Now()
			s.documents[doc.Name] = stored
		}
		response.WriteResults = append(response.WriteResults, &pb.WriteResult{UpdateTime: timestamppb.Now()})
	}
	return response, nil
}
func wireField(doc *pb.Document, path string) *pb.Value {
	if path == "__name__" {
		return &pb.Value{ValueType: &pb.Value_ReferenceValue{ReferenceValue: doc.Name}}
	}
	fields := doc.Fields
	var value *pb.Value
	for _, part := range strings.Split(path, ".") {
		value = fields[part]
		if value == nil {
			return nil
		}
		fields = value.GetMapValue().GetFields()
	}
	return value
}
func wireCompare(left, right *pb.Value) int {
	if proto.Equal(left, right) {
		return 0
	}
	if left.GetTimestampValue() != nil && right.GetTimestampValue() != nil {
		if left.GetTimestampValue().AsTime().Before(right.GetTimestampValue().AsTime()) {
			return -1
		}
		return 1
	}
	if _, ok := left.GetValueType().(*pb.Value_IntegerValue); ok {
		if left.GetIntegerValue() < right.GetIntegerValue() {
			return -1
		}
		return 1
	}
	l, r := left.GetStringValue(), right.GetStringValue()
	if l == "" {
		l = left.GetReferenceValue()
	}
	if r == "" {
		r = right.GetReferenceValue()
	}
	return strings.Compare(l, r)
}
func wireMatches(doc *pb.Document, filter *pb.StructuredQuery_Filter) (bool, error) {
	if filter == nil {
		return true, nil
	}
	if field := filter.GetFieldFilter(); field != nil {
		value := wireField(doc, field.Field.FieldPath)
		if value == nil {
			return false, status.Errorf(codes.InvalidArgument, "query field %q absent from SDK-encoded document", field.Field.FieldPath)
		}
		comparison := wireCompare(value, field.Value)
		switch field.Op {
		case pb.StructuredQuery_FieldFilter_EQUAL:
			return comparison == 0, nil
		case pb.StructuredQuery_FieldFilter_GREATER_THAN_OR_EQUAL:
			return comparison >= 0, nil
		default:
			return false, fmt.Errorf("unsupported fixture comparison")
		}
	}
	if composite := filter.GetCompositeFilter(); composite != nil {
		matched := composite.Op == pb.StructuredQuery_CompositeFilter_AND
		for _, child := range composite.Filters {
			ok, err := wireMatches(doc, child)
			if err != nil {
				return false, err
			}
			if composite.Op == pb.StructuredQuery_CompositeFilter_OR {
				matched = matched || ok
			} else {
				matched = matched && ok
			}
		}
		return matched, nil
	}
	return false, fmt.Errorf("unsupported fixture filter")
}
func (s *queryWireServer) RunQuery(request *pb.RunQueryRequest, stream pb.Firestore_RunQueryServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	query := request.GetStructuredQuery()
	if query == nil || len(query.From) != 1 {
		return status.Error(codes.InvalidArgument, "one collection required")
	}
	s.queries = append(s.queries, proto.Clone(query).(*pb.StructuredQuery))
	source := s.documents
	if len(request.GetTransaction()) > 0 {
		tx, ok := s.transactions[string(request.GetTransaction())]
		if !ok {
			return status.Error(codes.InvalidArgument, "query escaped transaction")
		}
		if s.beforeTransactionalQuery != nil {
			hook := s.beforeTransactionalQuery
			s.beforeTransactionalQuery = nil
			hook(s)
		}
		source = tx.documents
	}
	prefix := request.Parent + "/" + query.From[0].CollectionId + "/"
	var documents []*pb.Document
	for name, doc := range source {
		if !strings.HasPrefix(name, prefix) || strings.Contains(strings.TrimPrefix(name, prefix), "/") {
			continue
		}
		matches, err := wireMatches(doc, query.Where)
		if err != nil {
			return err
		}
		for _, order := range query.OrderBy {
			if wireField(doc, order.Field.FieldPath) == nil {
				return status.Errorf(codes.InvalidArgument, "order field %q absent from SDK-encoded document", order.Field.FieldPath)
			}
		}
		if matches {
			documents = append(documents, doc)
		}
	}
	sort.Slice(documents, func(i, j int) bool {
		for _, order := range query.OrderBy {
			comparison := wireCompare(wireField(documents[i], order.Field.FieldPath), wireField(documents[j], order.Field.FieldPath))
			if comparison != 0 {
				if order.Direction == pb.StructuredQuery_DESCENDING {
					return comparison > 0
				}
				return comparison < 0
			}
		}
		return documents[i].Name < documents[j].Name
	})
	if query.Limit != nil && int(query.Limit.Value) < len(documents) {
		documents = documents[:query.Limit.Value]
	}
	for _, doc := range documents {
		if err := stream.Send(&pb.RunQueryResponse{Document: doc, ReadTime: timestamppb.Now()}); err != nil {
			return err
		}
	}
	return nil
}
func queryWireFixture(t *testing.T) (*FirestoreStore, *queryWireServer) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	backend := &queryWireServer{quotaWireServer: &quotaWireServer{documents: map[string]*pb.Document{}, transactions: map[string]quotaWireTransaction{}}}
	pb.RegisterFirestoreServer(server, backend)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///query-wire", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client, err := firestore.NewClient(context.Background(), "query-test", option.WithGRPCConn(connection), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &FirestoreStore{client: client, projectID: "query-test"}, backend
}

func TestFirestoreQueriesMatchActualSDKPersistedModelFields(t *testing.T) {
	store, wire := queryWireFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	for _, put := range []func() error{
		func() error {
			return store.PutInstance(ctx, &models.Instance{ID: "instance", Plane: models.PlaneMCPlex, Runtime: models.TapeRuntime()})
		},
		func() error {
			return store.PutTemplate(ctx, &models.Template{ID: "template", Plane: models.PlaneMCPlex, Name: "Template"})
		},
		func() error { return store.PutAgent(ctx, &models.Agent{ClientID: "agent", RegisteredAt: now}) },
		func() error {
			return store.AppendHistory(ctx, &models.DeployHistory{ID: "history", InstanceID: "instance", Timestamp: now})
		},
		func() error { return store.AppendPolicyDenial(ctx, &models.PolicyDenial{ID: "denial", Timestamp: now}) },
		func() error {
			return store.PutRoleBinding(ctx, &models.RoleBinding{ID: "binding", Group: "engineering", CreatedAt: now})
		},
		func() error {
			return store.UpsertExecutionRun(ctx, &models.ExecutionRun{RunID: "run", TenantID: "tenant", AgentID: "agent"})
		},
		func() error {
			return store.AppendOperatorAudit(ctx, &models.OperatorAudit{ID: "audit", RunID: "run", At: now})
		},
	} {
		if err := put(); err != nil {
			t.Fatal(err)
		}
	}
	checks := []struct {
		name string
		call func() (int, error)
	}{
		{"instances", func() (int, error) { r, e := store.ListInstances(ctx, models.PlaneMCPlex); return len(r), e }},
		{"templates", func() (int, error) { r, _, e := store.ListTemplates(ctx, models.PlaneMCPlex, 0, 10); return len(r), e }},
		{"agents", func() (int, error) { r, e := store.ListAgents(ctx); return len(r), e }},
		{"history", func() (int, error) { r, e := store.ListHistory(ctx, "instance", 10); return len(r), e }},
		{"policy denials", func() (int, error) { r, e := store.ListPolicyDenials(ctx, 10); return len(r), e }},
		{"role binding order", func() (int, error) { r, e := store.ListRoleBindings(ctx); return len(r), e }},
		{"role binding group", func() (int, error) { r, e := store.ListRoleBindingsByGroup(ctx, "engineering"); return len(r), e }},
		{"execution runs", func() (int, error) { r, e := store.ListExecutionRuns(ctx, "tenant", "agent", 10); return len(r), e }},
		{"operator audit", func() (int, error) { r, e := store.ListOperatorAudit(ctx, "run", 10); return len(r), e }},
		{"runtime count", func() (int, error) { return store.CountInstancesWithRuntime(ctx, models.RuntimeEngineTape) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			count, err := check.call()
			if err != nil || count != 1 {
				t.Fatalf("persisted model missing from query: count=%d err=%v", count, err)
			}
		})
	}
	wire.mu.Lock()
	defer wire.mu.Unlock()
	for _, doc := range wire.documents {
		if strings.Contains(doc.Name, "/instances/") {
			if doc.Fields["Plane"] == nil || doc.Fields["plane"] != nil || wireField(doc, "Runtime.Engine") == nil {
				t.Fatalf("unexpected SDK storage schema: %+v", doc.Fields)
			}
		}
	}
}

func TestFirestoreUsageFiltersPrecedeLimitAndSummary(t *testing.T) {
	store, _ := queryWireFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	for _, record := range []models.UsageRecord{
		{ID: "target", ModelID: "model", AgentID: "agent", Timestamp: now, InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		{ID: "other-model", ModelID: "other", AgentID: "agent", Timestamp: now.Add(time.Second), TotalTokens: 90},
		{ID: "other-agent", ModelID: "model", AgentID: "other", Timestamp: now.Add(2 * time.Second), TotalTokens: 90},
	} {
		if err := store.AppendUsage(ctx, &record); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListUsageRecords(ctx, "model", "agent", now.Add(-time.Hour), 1)
	if err != nil || len(rows) != 1 || rows[0].ID != "target" {
		t.Fatalf("unrelated newer usage hid requested page: %+v %v", rows, err)
	}
	summary, err := store.GetUsageSummary(ctx, "model", "agent", "day")
	if err != nil || summary.RequestCount != 1 || summary.TotalTokens != 5 {
		t.Fatalf("incorrect scoped usage summary: %+v %v", summary, err)
	}
}

func TestFirestoreExecutionEventsUseNumericSequenceOrder(t *testing.T) {
	store, _ := queryWireFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, seq := range []int64{10, 1, 2} {
		if _, err := store.AppendExecutionEvent(ctx, &models.ExecutionEvent{RunID: "run", Seq: seq}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListExecutionEvents(ctx, "run", 0, 3)
	if err != nil || len(rows) != 3 {
		t.Fatalf("sequence query: %+v %v", rows, err)
	}
	for index, want := range []int64{1, 2, 10} {
		if rows[index].Seq != want {
			t.Fatalf("sequence sorted as document-name text: %+v", rows)
		}
	}
}

func TestFirestoreIndexDefinitionsMatchActualSDKFields(t *testing.T) {
	store, wire := queryWireFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for collection, model := range map[string]any{
		"instances": models.Instance{}, "templates": models.Template{},
		"deploy_history": models.DeployHistory{}, "usage_records": models.UsageRecord{},
		"execution_runs": models.ExecutionRun{}, "delegations": models.Delegation{},
		"skill_invocations": models.SkillInvocation{},
	} {
		if _, err := store.client.Collection(collection).Doc("schema").Set(ctx, model); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.ReadFile("../../deploy/terraform/firestore.tf")
	if err != nil {
		t.Fatal(err)
	}
	header := regexp.MustCompile(`resource "google_firestore_index" "([^"]+)" \{`)
	collectionPattern := regexp.MustCompile(`collection\s*=\s*"([^"]+)"`)
	fieldPattern := regexp.MustCompile(`field_path\s*=\s*"([^"]+)"`)
	blocks := header.FindAllSubmatchIndex(source, -1)
	indexes := map[string][]string{}
	for index, block := range blocks {
		end := len(source)
		if index+1 < len(blocks) {
			end = blocks[index+1][0]
		}
		body := source[block[1]:end]
		name := string(source[block[2]:block[3]])
		collection := collectionPattern.FindSubmatch(body)
		if len(collection) != 2 {
			t.Fatalf("index %s has no collection", name)
		}
		var document *pb.Document
		for path, candidate := range wire.documents {
			if strings.Contains(path, "/"+string(collection[1])+"/") {
				document = candidate
				break
			}
		}
		if document == nil {
			t.Fatalf("index %s collection has no SDK schema fixture", name)
		}
		for _, field := range fieldPattern.FindAllSubmatch(body, -1) {
			path := string(field[1])
			if wireField(document, path) == nil {
				t.Fatalf("Terraform index %s uses field %q absent from SDK Commit document", name, path)
			}
			indexes[name] = append(indexes[name], path)
		}
	}
	for name, fields := range map[string][]string{
		"history_by_instance":            {"InstanceID", "Timestamp"},
		"templates_by_plane":             {"Plane", "Name"},
		"usage_by_model":                 {"ModelID", "Timestamp"},
		"usage_by_agent":                 {"AgentID", "Timestamp"},
		"usage_by_model_agent":           {"ModelID", "AgentID", "Timestamp"},
		"execution_runs_by_tenant_agent": {"TenantID", "AgentID"},
		"delegations_by_caller":          {"CallerAgentID", "StartedAt"},
		"delegations_by_callee":          {"CalleeAgentID", "StartedAt"},
		"delegations_by_trace":           {"TraceID", "StartedAt"},
		"skills_by_agent":                {"AgentID", "StartedAt"},
		"skills_by_name":                 {"SkillName", "StartedAt"},
		"skills_by_agent_name":           {"AgentID", "SkillName", "StartedAt"},
	} {
		if !reflect.DeepEqual(indexes[name], fields) {
			t.Errorf("index %s fields=%v, query requires %v", name, indexes[name], fields)
		}
	}
}
