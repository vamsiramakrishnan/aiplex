package registry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	pb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ledgerWireServer evaluates the real SDK's Commit, BatchGet and RunQuery
// wire requests (filters, orders, StartAfter cursors, limits) for the usage
// ledger without an emulator, in the style of the query cohesion fixture.
type ledgerWireServer struct {
	pb.UnimplementedFirestoreServer
	mu        sync.Mutex
	documents map[string]*pb.Document
	queries   int
}

func (s *ledgerWireServer) Commit(_ context.Context, r *pb.CommitRequest) (*pb.CommitResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := timestamppb.Now()
	out := &pb.CommitResponse{CommitTime: now}
	for _, write := range r.Writes {
		doc := write.GetUpdate()
		if doc == nil {
			return nil, status.Error(codes.InvalidArgument, "ledger writes are creates only")
		}
		if exists, ok := write.GetCurrentDocument().GetConditionType().(*pb.Precondition_Exists); !ok || exists.Exists {
			return nil, status.Error(codes.InvalidArgument, "ledger appends must be create-only")
		}
		if s.documents[doc.Name] != nil {
			return nil, status.Error(codes.AlreadyExists, "document exists")
		}
		stored := proto.Clone(doc).(*pb.Document)
		stored.CreateTime, stored.UpdateTime = now, now
		s.documents[doc.Name] = stored
		out.WriteResults = append(out.WriteResults, &pb.WriteResult{UpdateTime: now})
	}
	return out, nil
}

func (s *ledgerWireServer) BatchGetDocuments(r *pb.BatchGetDocumentsRequest, stream pb.Firestore_BatchGetDocumentsServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range r.Documents {
		out := &pb.BatchGetDocumentsResponse{ReadTime: timestamppb.Now()}
		if doc := s.documents[name]; doc != nil {
			out.Result = &pb.BatchGetDocumentsResponse_Found{Found: proto.Clone(doc).(*pb.Document)}
		} else {
			out.Result = &pb.BatchGetDocumentsResponse_Missing{Missing: name}
		}
		if err := stream.Send(out); err != nil {
			return err
		}
	}
	return nil
}

func ledgerWireMatch(doc *pb.Document, filter *pb.StructuredQuery_Filter) (bool, error) {
	if filter == nil {
		return true, nil
	}
	if composite := filter.GetCompositeFilter(); composite != nil {
		for _, child := range composite.Filters {
			ok, err := ledgerWireMatch(doc, child)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	field := filter.GetFieldFilter()
	if field == nil {
		return false, fmt.Errorf("unsupported filter")
	}
	value := wireField(doc, field.Field.FieldPath)
	if value == nil {
		return false, status.Errorf(codes.InvalidArgument, "filter field %q absent", field.Field.FieldPath)
	}
	c := wireCompare(value, field.Value)
	switch field.Op {
	case pb.StructuredQuery_FieldFilter_GREATER_THAN_OR_EQUAL:
		return c >= 0, nil
	case pb.StructuredQuery_FieldFilter_LESS_THAN:
		return c < 0, nil
	}
	return false, fmt.Errorf("unsupported comparison %v", field.Op)
}

func (s *ledgerWireServer) RunQuery(r *pb.RunQueryRequest, stream pb.Firestore_RunQueryServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++
	q := r.GetStructuredQuery()
	if q == nil || len(q.From) != 1 || q.From[0].AllDescendants {
		return status.Error(codes.InvalidArgument, "one tenant collection required")
	}
	prefix := r.Parent + "/" + q.From[0].CollectionId + "/"
	var docs []*pb.Document
	for name, doc := range s.documents {
		if !strings.HasPrefix(name, prefix) || strings.Contains(strings.TrimPrefix(name, prefix), "/") {
			continue
		}
		ok, err := ledgerWireMatch(doc, q.Where)
		if err != nil {
			return err
		}
		if ok {
			docs = append(docs, doc)
		}
	}
	compare := func(doc *pb.Document, values []*pb.Value) int {
		for i, order := range q.OrderBy {
			if i >= len(values) {
				break
			}
			c := wireCompare(wireField(doc, order.Field.FieldPath), values[i])
			if order.Direction == pb.StructuredQuery_DESCENDING {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		return 0
	}
	sort.Slice(docs, func(i, j int) bool {
		values := []*pb.Value{}
		for _, order := range q.OrderBy {
			values = append(values, wireField(docs[j], order.Field.FieldPath))
		}
		return compare(docs[i], values) < 0
	})
	if cursor := q.StartAt; cursor != nil {
		kept := docs[:0]
		for _, doc := range docs {
			c := compare(doc, cursor.Values)
			if c > 0 || (c == 0 && cursor.Before) {
				kept = append(kept, doc)
			}
		}
		docs = kept
	}
	if q.Limit != nil && int(q.Limit.Value) < len(docs) {
		docs = docs[:q.Limit.Value]
	}
	for _, doc := range docs {
		if err := stream.Send(&pb.RunQueryResponse{Document: doc, ReadTime: timestamppb.Now()}); err != nil {
			return err
		}
	}
	return nil
}

func ledgerWireFixture(t *testing.T) (*FirestoreStore, *ledgerWireServer) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	backend := &ledgerWireServer{documents: map[string]*pb.Document{}}
	pb.RegisterFirestoreServer(server, backend)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///ledger-wire", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client, err := firestore.NewClient(context.Background(), "ledger-test", option.WithGRPCConn(conn), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &FirestoreStore{client: client, projectID: "ledger-test"}, backend
}

func ledgerRecord(tenant, id string, at time.Time) usageledger.Record {
	return usageledger.Record{ID: id, TenantID: tenant, StartedAt: at, FinishedAt: at.Add(time.Second), Plane: usageledger.PlaneLLM, Provider: "google", ModelID: "m", PrincipalID: "alice", Status: usageledger.StatusOK, Source: usageledger.SourceManaged, Requests: 1, Usage: &usageledger.Usage{InputTokens: usageledger.Int64(7)}, Cost: usageledger.Cost{Basis: usageledger.BasisSettled, Currency: "USD", Amount: "123456789012345678901234567890", Scale: 18}}
}

func exerciseUsageLedgerStore(t *testing.T, store usageledger.Store, now time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ids := []string{}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("r%d", i)
		// Pairs share a timestamp to exercise the (started_at, id) cursor.
		if err := store.Append(ctx, ledgerRecord("tenant-a", id, now.Add(-time.Duration(10-i/2)*time.Minute))); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := store.Append(ctx, ledgerRecord("tenant-b", "foreign", now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, ledgerRecord("tenant-a", "r0", now)); !errors.Is(err, usageledger.ErrConflict) {
		t.Fatalf("duplicate append must conflict: %v", err)
	}
	if err := store.Append(ctx, usageledger.Record{ID: "bad", TenantID: "tenant-a"}); !errors.Is(err, usageledger.ErrInvalid) {
		t.Fatalf("invalid record stored: %v", err)
	}
	got, err := store.Get(ctx, "tenant-a", "r3")
	if err != nil || got.Cost.Amount != "123456789012345678901234567890" || got.Usage == nil || *got.Usage.InputTokens != 7 || got.Usage.OutputTokens != nil {
		t.Fatalf("exact roundtrip: %+v %v", got, err)
	}
	if _, err := store.Get(ctx, "tenant-b", "r3"); !errors.Is(err, usageledger.ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	var listed []string
	cursor := ""
	for {
		page, err := store.List(ctx, "tenant-a", usageledger.Query{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Records {
			if r.TenantID != "tenant-a" {
				t.Fatal("foreign tenant listed")
			}
			listed = append(listed, r.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if strings.Join(listed, ",") != "r6,r5,r4,r3,r2,r1,r0" {
		t.Fatalf("newest-first paging: %v", listed)
	}
	page, err := store.List(ctx, "tenant-a", usageledger.Query{Filter: usageledger.Filter{Since: now.Add(-9 * time.Minute), Until: now.Add(-7*time.Minute - time.Second), Visibility: []string{"alice"}}})
	if err != nil || len(page.Records) != 4 {
		t.Fatalf("window filter: %+v %v", page.Records, err)
	}
	page, _ = store.List(ctx, "tenant-a", usageledger.Query{Filter: usageledger.Filter{Visibility: []string{"mallory"}}})
	if len(page.Records) != 0 {
		t.Fatal("visibility ignored")
	}
}

func TestUsageLedgerFirestoreWire(t *testing.T) {
	store, backend := ledgerWireFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	ledger := store.UsageLedger(time.Hour)
	exerciseUsageLedgerStore(t, ledger, now)
	backend.mu.Lock()
	for name := range backend.documents {
		if !strings.Contains(name, "/usage_ledger_tenants/") || !strings.Contains(name, "/usage_ledger_records/") || strings.Contains(name, "tenant-a") {
			t.Errorf("ledger document outside the digest-keyed tenant layout: %s", name)
		}
	}
	backend.mu.Unlock()
	// Records older than retention are neither listed nor readable.
	if err := ledger.Append(context.Background(), ledgerRecord("tenant-a", "ancient", now.Add(-2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Get(context.Background(), "tenant-a", "ancient"); !errors.Is(err, usageledger.ErrNotFound) {
		t.Fatalf("expired record readable: %v", err)
	}
	page, _ := ledger.List(context.Background(), "tenant-a", usageledger.Query{Limit: usageledger.MaxLimit})
	for _, r := range page.Records {
		if r.ID == "ancient" {
			t.Fatal("expired record listed")
		}
	}
}

// A sparse filter crosses query batches (StartAfter on the last scanned
// document) and reaches the oldest matching record.
func TestUsageLedgerFirestoreScansAcrossBatches(t *testing.T) {
	store, backend := ledgerWireFixture(t)
	ledger := store.UsageLedger(time.Hour)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	target := ledgerRecord("tenant-c", "target", now.Add(-30*time.Minute))
	target.PrincipalID = "needle"
	if err := ledger.Append(ctx, target); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < usageLedgerBatch+20; i++ {
		if err := ledger.Append(ctx, ledgerRecord("tenant-c", fmt.Sprintf("bulk-%04d", i), now.Add(-time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	backend.mu.Lock()
	before := backend.queries
	backend.mu.Unlock()
	page, err := ledger.List(ctx, "tenant-c", usageledger.Query{Filter: usageledger.Filter{PrincipalID: "needle"}})
	if err != nil || len(page.Records) != 1 || page.Records[0].ID != "target" || page.NextCursor != "" {
		t.Fatalf("sparse scan: %+v %v", page, err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.queries-before < 2 {
		t.Fatalf("expected a second batch query, got %d", backend.queries-before)
	}
}

func TestUsageLedgerFirestoreRejectsForeignStoredRecord(t *testing.T) {
	doc, err := usageLedgerDocument(ledgerRecord("tenant-a", "r", time.Now()), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usageLedgerFromDoc(doc, "tenant-b"); err == nil {
		t.Fatal("record from another tenant accepted")
	}
	doc.Record = "not json"
	if _, err := usageLedgerFromDoc(doc, "tenant-a"); err == nil {
		t.Fatal("malformed stored record accepted")
	}
}

func TestUsageLedgerFirestoreEmulator(t *testing.T) {
	skipWithoutEmulator(t)
	store, err := NewFirestoreStore(fmt.Sprintf("ledger-%d", time.Now().UnixNano()), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exerciseUsageLedgerStore(t, store.UsageLedger(time.Hour), time.Now().UTC().Truncate(time.Millisecond))
}

func TestUsageLedgerMemoryStoreMatchesContract(t *testing.T) {
	exerciseUsageLedgerStore(t, usageledger.NewMemoryStore(usageledger.WithRetention(time.Hour)), time.Now().UTC().Truncate(time.Millisecond))
}
