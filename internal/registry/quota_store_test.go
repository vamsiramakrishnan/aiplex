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
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A narrow transactional server verifies the actual Go SDK wire protocol. Each
// Begin creates an isolated read snapshot; injected commit aborts exercise SDK
// callback retries without a cloud deployment or emulator dependency.
type quotaWireTransaction struct {
	documents map[string]*pb.Document
	readOnly  bool
}
type quotaWireServer struct {
	pb.UnimplementedFirestoreServer
	mu             sync.Mutex
	documents      map[string]*pb.Document
	transactions   map[string]quotaWireTransaction
	next           int
	abortCommits   int
	commitFailure  codes.Code
	commits        int
	reads          int
	readBatchSizes []int
	readOnlyViews  int
}

func (s *quotaWireServer) BeginTransaction(_ context.Context, r *pb.BeginTransactionRequest) (*pb.BeginTransactionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := fmt.Sprint(s.next)
	docs := map[string]*pb.Document{}
	for name, doc := range s.documents {
		docs[name] = proto.Clone(doc).(*pb.Document)
	}
	readOnly := r.Options.GetReadOnly() != nil
	if readOnly {
		s.readOnlyViews++
	}
	s.transactions[id] = quotaWireTransaction{documents: docs, readOnly: readOnly}
	return &pb.BeginTransactionResponse{Transaction: []byte(id)}, nil
}
func (s *quotaWireServer) Rollback(_ context.Context, r *pb.RollbackRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.transactions, string(r.Transaction))
	return &emptypb.Empty{}, nil
}
func (s *quotaWireServer) BatchGetDocuments(r *pb.BatchGetDocumentsRequest, stream pb.Firestore_BatchGetDocumentsServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.transactions[string(r.GetTransaction())]
	if !ok {
		return status.Error(codes.InvalidArgument, "read escaped transaction")
	}
	s.reads++
	s.readBatchSizes = append(s.readBatchSizes, len(r.Documents))
	for _, name := range r.Documents {
		out := &pb.BatchGetDocumentsResponse{ReadTime: timestamppb.Now()}
		if doc := tx.documents[name]; doc != nil {
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
func (s *quotaWireServer) Commit(_ context.Context, r *pb.CommitRequest) (*pb.CommitResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.transactions[string(r.Transaction)]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "commit escaped transaction")
	}
	if tx.readOnly && len(r.Writes) != 0 {
		return nil, status.Error(codes.InvalidArgument, "writes in read-only view")
	}
	if len(r.Writes) > 0 && s.commitFailure != codes.OK {
		return nil, status.Error(s.commitFailure, "fixture commit denied")
	}
	if len(r.Writes) > 0 && s.abortCommits > 0 {
		s.abortCommits--
		return nil, status.Error(codes.Aborted, "fixture contention")
	}
	// Check every create condition before modifying any document.
	for _, write := range r.Writes {
		if doc := write.GetUpdate(); doc != nil && write.CurrentDocument != nil {
			if exists, ok := write.CurrentDocument.ConditionType.(*pb.Precondition_Exists); ok && (s.documents[doc.Name] != nil) != exists.Exists {
				return nil, status.Error(codes.AlreadyExists, "create collision")
			}
		}
	}
	s.commits++
	now := timestamppb.Now()
	out := &pb.CommitResponse{CommitTime: now}
	for _, write := range r.Writes {
		if doc := write.GetUpdate(); doc != nil {
			copy := proto.Clone(doc).(*pb.Document)
			copy.CreateTime, copy.UpdateTime = now, now
			s.documents[doc.Name] = copy
		} else if name := write.GetDelete(); name != "" {
			delete(s.documents, name)
		}
		out.WriteResults = append(out.WriteResults, &pb.WriteResult{UpdateTime: now})
	}
	delete(s.transactions, string(r.Transaction))
	return out, nil
}
func (s *quotaWireServer) RunQuery(r *pb.RunQueryRequest, stream pb.Firestore_RunQueryServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.transactions[string(r.GetTransaction())]
	if !ok {
		return status.Error(codes.InvalidArgument, "query escaped transaction")
	}
	query := r.GetStructuredQuery()
	if len(query.From) != 1 || query.From[0].CollectionId != "records" || query.Where != nil || len(query.OrderBy) == 0 || query.OrderBy[0].Field.FieldPath != "id" || query.OrderBy[0].Direction != pb.StructuredQuery_ASCENDING {
		return status.Error(codes.InvalidArgument, "unexpected query")
	}
	prefix := r.Parent + "/records/"
	var names []string
	after := ""
	if query.StartAt != nil {
		if query.StartAt.Before || len(query.StartAt.Values) != 1 {
			return status.Error(codes.InvalidArgument, "unexpected scan cursor")
		}
		after = query.StartAt.Values[0].GetStringValue()
	}
	for name, doc := range tx.documents {
		if strings.HasPrefix(name, prefix) && !strings.Contains(strings.TrimPrefix(name, prefix), "/") {
			if doc.Fields["id"].GetStringValue() > after {
				names = append(names, name)
			}
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return tx.documents[names[i]].Fields["id"].GetStringValue() < tx.documents[names[j]].Fields["id"].GetStringValue()
	})
	if query.Limit != nil && len(names) > int(query.Limit.Value) {
		names = names[:query.Limit.Value]
	}
	for _, name := range names {
		if err := stream.Send(&pb.RunQueryResponse{Document: proto.Clone(tx.documents[name]).(*pb.Document), ReadTime: timestamppb.Now()}); err != nil {
			return err
		}
	}
	return nil
}
func quotaWireFixture(t *testing.T) (*FirestoreStore, *quotaWireServer) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	backend := &quotaWireServer{documents: map[string]*pb.Document{}, transactions: map[string]quotaWireTransaction{}}
	pb.RegisterFirestoreServer(server, backend)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///quota-wire", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client, err := firestore.NewClient(context.Background(), "quota-test", option.WithGRPCConn(connection), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &FirestoreStore{client: client}, backend
}

func TestFirestoreQuotaTransactionsStageWritesAndRetryFreshState(t *testing.T) {
	store, backend := quotaWireFixture(t)
	quota := store.QuotaStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := quota.Update(ctx, "tenant", func(tx quotaplex.Tx) error { return tx.Create("balances", "shared", 10) }); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.abortCommits = 1
	backend.mu.Unlock()
	callbacks := 0
	if err := quota.Update(ctx, "tenant", func(tx quotaplex.Tx) error {
		callbacks++
		// A later read after staged Create must still be a native read-before-write.
		if err := tx.Create("attempts", "one", map[string]int{"held": 3}); err != nil {
			return err
		}
		var available int
		if err := tx.Get("balances", "shared", &available); err != nil {
			return err
		}
		return tx.Put("balances", "shared", available-3)
	}); err != nil {
		t.Fatal(err)
	}
	if callbacks != 2 {
		t.Fatalf("callbacks=%d", callbacks)
	}
	// Recreate the adapter to demonstrate state is in the backend, not local maps.
	if err := store.QuotaStore().View(ctx, "tenant", func(tx quotaplex.Tx) error {
		var available int
		if err := tx.Get("balances", "shared", &available); err != nil {
			return err
		}
		attempts, err := tx.List("attempts")
		if err != nil {
			return err
		}
		if available != 7 || len(attempts) != 1 {
			t.Fatalf("available=%d attempts=%d", available, len(attempts))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	views, reads := backend.readOnlyViews, backend.reads
	backend.mu.Unlock()
	if views == 0 || reads < 5 {
		t.Fatalf("readOnly=%d transactional reads=%d", views, reads)
	}
}

func TestFirestoreQuotaTransactionsRollbackFailuresAndRejectNamespaceDrift(t *testing.T) {
	store, backend := quotaWireFixture(t)
	quota := store.QuotaStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := quota.Update(ctx, "t", func(tx quotaplex.Tx) error { return tx.Create("balances", "one", 10) }); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("later constraint denied")
	if err := quota.Update(ctx, "t", func(tx quotaplex.Tx) error { _ = tx.Put("balances", "one", 0); return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := quota.View(ctx, "t", func(tx quotaplex.Tx) error { _ = tx.Put("balances", "one", 0); return nil }); !errors.Is(err, quotaplex.ErrReadOnly) {
		t.Fatal(err)
	}
	if err := quota.Update(ctx, "t", func(tx quotaplex.Tx) error {
		_ = tx.Put("balances", "one", 0)
		_ = tx.Create("balances", "one", 20)
		return nil
	}); !errors.Is(err, quotaplex.ErrConflict) {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.commitFailure = codes.PermissionDenied
	backend.mu.Unlock()
	if err := quota.Update(ctx, "t", func(tx quotaplex.Tx) error { return tx.Put("balances", "one", 1) }); status.Code(err) != codes.PermissionDenied {
		t.Fatal("commit error hidden", err)
	}
	backend.mu.Lock()
	backend.commitFailure = codes.OK
	backend.mu.Unlock()
	if err := quota.View(ctx, "t", func(tx quotaplex.Tx) error {
		var value int
		if err := tx.Get("balances", "one", &value); err != nil {
			return err
		}
		if value != 10 {
			t.Fatalf("failed transaction committed %d", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	for _, doc := range backend.documents {
		doc.Fields["tenant"] = &pb.Value{ValueType: &pb.Value_StringValue{StringValue: "other"}}
	}
	backend.mu.Unlock()
	for _, operation := range []func(quotaplex.Tx) error{
		func(tx quotaplex.Tx) error { var value int; return tx.Get("balances", "one", &value) },
		func(tx quotaplex.Tx) error { return tx.Put("balances", "one", 2) },
		func(tx quotaplex.Tx) error { _, err := tx.List("balances"); return err },
	} {
		if err := quota.Update(ctx, "t", operation); err == nil {
			t.Fatal("namespace corruption accepted")
		}
	}
}

func TestFirestoreQuotaCoordinatesIsolationClosedHandlesAndBoundedInventory(t *testing.T) {
	store, _ := quotaWireFixture(t)
	quota := store.QuotaStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var escaped quotaplex.Tx
	if err := quota.Update(ctx, "tenant/slash", func(tx quotaplex.Tx) error { escaped = tx; return tx.Create("kind/slash", "record/slash", 1) }); err != nil {
		t.Fatal(err)
	}
	if err := escaped.Put("kind/slash", "record/slash", 2); !errors.Is(err, quotaplex.ErrTransactionClosed) {
		t.Fatal(err)
	}
	if err := quota.View(ctx, "tenant", func(tx quotaplex.Tx) error { var v int; return tx.Get("slash/kind", "slash/record/slash", &v) }); !errors.Is(err, quotaplex.ErrNotFound) {
		t.Fatal("coordinate alias", err)
	}
	for i := 0; i <= quotaplex.MaxTransactionRecords; i++ {
		if err := quota.Update(ctx, "t", func(tx quotaplex.Tx) error { return tx.Put("many", fmt.Sprint(i), i) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := quota.View(ctx, "t", func(tx quotaplex.Tx) error { _, err := tx.List("many"); return err }); !errors.Is(err, quotaplex.ErrTransactionLimit) {
		t.Fatal("inventory silently truncated", err)
	}
	var all []string
	after := ""
	for {
		var page []quotaplex.Record
		if err := quota.View(ctx, "t", func(tx quotaplex.Tx) error { var err error; page, err = tx.Scan("many", after, 64); return err }); err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, record := range page {
			if record.ID <= after {
				t.Fatal("unordered/duplicate scan")
			}
			all = append(all, record.ID)
			after = record.ID
		}
	}
	if len(all) != quotaplex.MaxTransactionRecords+1 {
		t.Fatalf("paged recovery lost records: %d", len(all))
	}
}

func TestQuotaStoreScanMergesStagedStateOnBothBackends(t *testing.T) {
	for _, name := range []string{"memory", "firestore"} {
		t.Run(name, func(t *testing.T) {
			var store quotaplex.Store = quotaplex.NewMemoryStore()
			if name == "firestore" {
				registry, _ := quotaWireFixture(t)
				store = registry.QuotaStore()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := store.Update(ctx, "t", func(tx quotaplex.Tx) error {
				for _, id := range []string{"a", "c", "e"} {
					if err := tx.Create("values", id, id); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.Update(ctx, "t", func(tx quotaplex.Tx) error {
				if err := tx.Delete("values", "c"); err != nil {
					return err
				}
				if err := tx.Create("values", "b", "new-b"); err != nil {
					return err
				}
				if err := tx.Put("values", "e", "new-e"); err != nil {
					return err
				}
				page, err := tx.Scan("values", "a", 2)
				if err != nil {
					return err
				}
				if len(page) != 2 || page[0].ID != "b" || page[1].ID != "e" || string(page[1].Value) != `"new-e"` {
					t.Fatalf("staged page=%+v", page)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQuotaPrefetchCoalescesReadsAndPreservesStagedState(t *testing.T) {
	for _, name := range []string{"memory", "firestore"} {
		t.Run(name, func(t *testing.T) {
			var store quotaplex.Store = quotaplex.NewMemoryStore()
			var wire *quotaWireServer
			if name == "firestore" {
				registry, backend := quotaWireFixture(t)
				store, wire = registry.QuotaStore(), backend
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := store.Update(ctx, "t", func(tx quotaplex.Tx) error {
				for i, id := range []string{"a", "b", "c"} {
					if err := tx.Create("values", id, i+1); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			readCount := func() int {
				if wire == nil {
					return 0
				}
				wire.mu.Lock()
				defer wire.mu.Unlock()
				return wire.reads
			}
			before := readCount()
			var escaped quotaplex.Tx
			if err := store.View(ctx, "t", func(tx quotaplex.Tx) error {
				escaped = tx
				keys := []quotaplex.Key{{Kind: "values", ID: "a"}, {Kind: "values", ID: "c"}, {Kind: "values", ID: "missing"}, {Kind: "values", ID: "a"}}
				if err := tx.Prefetch(keys); err != nil {
					return err
				}
				for _, key := range keys {
					var value int
					err := tx.Get(key.Kind, key.ID, &value)
					if key.ID == "missing" {
						if !errors.Is(err, quotaplex.ErrNotFound) {
							t.Fatal("missing record was fabricated", err)
						}
						continue
					}
					if err != nil {
						return err
					}
					if key.ID == "a" && value != 1 || key.ID == "c" && value != 3 {
						t.Fatal("wrong prefetched value", key, value)
					}
				}
				return tx.Prefetch(keys)
			}); err != nil {
				t.Fatal(err)
			}
			if err := escaped.Prefetch(nil); !errors.Is(err, quotaplex.ErrTransactionClosed) {
				t.Fatal("escaped prefetch accepted", err)
			}
			if wire != nil {
				wire.mu.Lock()
				calls, batch := wire.reads-before, wire.readBatchSizes[len(wire.readBatchSizes)-1]
				wire.mu.Unlock()
				if calls != 1 || batch != 3 {
					t.Fatalf("prefetch failed coalescing/cache: calls=%d batch=%d", calls, batch)
				}
			}
			before = readCount()
			if err := store.Update(ctx, "t", func(tx quotaplex.Tx) error {
				if err := tx.Put("values", "a", 11); err != nil {
					return err
				}
				if err := tx.Create("values", "new", 14); err != nil {
					return err
				}
				if err := tx.Delete("values", "b"); err != nil {
					return err
				}
				keys := []quotaplex.Key{{Kind: "values", ID: "a"}, {Kind: "values", ID: "b"}, {Kind: "values", ID: "new"}, {Kind: "values", ID: "c"}, {Kind: "values", ID: "missing"}, {Kind: "values", ID: "c"}}
				if err := tx.Prefetch(keys); err != nil {
					return err
				}
				for _, expected := range []struct {
					id    string
					value int
				}{{"a", 11}, {"new", 14}, {"c", 3}} {
					var value int
					if err := tx.Get("values", expected.id, &value); err != nil {
						return err
					}
					if value != expected.value {
						t.Fatal("prefetch overwrote staged value", expected, value)
					}
				}
				var missing int
				if err := tx.Get("values", "b", &missing); !errors.Is(err, quotaplex.ErrNotFound) {
					t.Fatal("prefetch resurrected staged deletion", err)
				}
				return tx.Create("values", "missing", 15)
			}); err != nil {
				t.Fatal(err)
			}
			if wire != nil {
				wire.mu.Lock()
				calls, batch := wire.reads-before, wire.readBatchSizes[len(wire.readBatchSizes)-1]
				wire.mu.Unlock()
				if calls != 4 || batch != 2 {
					t.Fatalf("staged prefetch read extra records: calls=%d last batch=%d", calls, batch)
				}
			}
		})
	}
}

func TestQuotaPrefetchRejectsUnboundedOrCorruptInputsAtomically(t *testing.T) {
	registry, wire := quotaWireFixture(t)
	store := registry.QuotaStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Update(ctx, "t", func(tx quotaplex.Tx) error { return tx.Create("values", "one", 1) }); err != nil {
		t.Fatal(err)
	}
	for _, keys := range [][]quotaplex.Key{
		make([]quotaplex.Key, quotaplex.MaxTransactionRecords+1),
		{{Kind: "values", ID: "one"}, {Kind: "values", ID: "invalid\x00coordinate"}},
	} {
		wire.mu.Lock()
		before := wire.reads
		wire.mu.Unlock()
		if err := store.Update(ctx, "t", func(tx quotaplex.Tx) error { _ = tx.Prefetch(keys); return nil }); err == nil {
			t.Fatal("ignored prefetch error committed")
		}
		wire.mu.Lock()
		after := wire.reads
		wire.mu.Unlock()
		if before != after {
			t.Fatal("invalid bulk coordinates issued RPC")
		}
	}
	wire.mu.Lock()
	for _, doc := range wire.documents {
		doc.Fields["tenant"] = &pb.Value{ValueType: &pb.Value_StringValue{StringValue: "wrong-tenant"}}
	}
	wire.mu.Unlock()
	if err := store.View(ctx, "t", func(tx quotaplex.Tx) error { return tx.Prefetch([]quotaplex.Key{{Kind: "values", ID: "one"}}) }); err == nil {
		t.Fatal("bulk cache accepted corrupted namespace")
	}
}
