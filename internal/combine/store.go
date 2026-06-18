package combine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	sidecarv1 "github.com/defenseunicorns/keycloak-portal/internal/peat/sidecarv1"
)

const collection = "combined_sources"

// Store persists combined-source specs (source of truth: peat; fake: memory).
type Store interface {
	Put(ctx context.Context, c Combined) error
	Get(ctx context.Context, key string) (Combined, bool, error)
	List(ctx context.Context) ([]Combined, error)
	Delete(ctx context.Context, key string) error
}

// PeatStore persists combined-source specs in the peat mesh.
type PeatStore struct {
	conn   *grpc.ClientConn
	client sidecarv1.PeatSidecarClient
}

// NewPeatStore dials the peat sidecar at addr. tlsCreds may be nil (plaintext).
func NewPeatStore(addr string, tlsCreds credentials.TransportCredentials) (*PeatStore, error) {
	creds := tlsCreds
	if creds == nil {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dialing peat node %q: %w", addr, err)
	}
	return &PeatStore{conn: conn, client: sidecarv1.NewPeatSidecarClient(conn)}, nil
}

// Close releases the gRPC connection.
func (s *PeatStore) Close() error { return s.conn.Close() }

func (s *PeatStore) Put(ctx context.Context, c Combined) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.client.PutDocument(ctx, &sidecarv1.PutDocumentRequest{Collection: collection, DocId: c.Key, JsonData: string(data)})
	if err != nil {
		return fmt.Errorf("peat PutDocument(%s/%s): %w", collection, c.Key, err)
	}
	return nil
}

func (s *PeatStore) Get(ctx context.Context, key string) (Combined, bool, error) {
	got, err := s.client.GetDocument(ctx, &sidecarv1.GetDocumentRequest{Collection: collection, DocId: key})
	if err != nil {
		return Combined{}, false, fmt.Errorf("peat GetDocument(%s/%s): %w", collection, key, err)
	}
	if got.JsonData == nil {
		return Combined{}, false, nil
	}
	var c Combined
	if err := json.Unmarshal([]byte(*got.JsonData), &c); err != nil {
		return Combined{}, false, err
	}
	return c, true, nil
}

func (s *PeatStore) List(ctx context.Context) ([]Combined, error) {
	resp, err := s.client.ListDocuments(ctx, &sidecarv1.ListDocumentsRequest{Collection: collection})
	if err != nil {
		return nil, fmt.Errorf("peat ListDocuments(%s): %w", collection, err)
	}
	ids := append([]string(nil), resp.DocIds...)
	sort.Strings(ids)
	out := make([]Combined, 0, len(ids))
	for _, id := range ids {
		c, ok, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *PeatStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteDocument(ctx, &sidecarv1.DeleteDocumentRequest{Collection: collection, DocId: key})
	if err != nil {
		return fmt.Errorf("peat DeleteDocument(%s/%s): %w", collection, key, err)
	}
	return nil
}

// MemoryStore is an in-memory spec store for tests and no-peat runs.
type MemoryStore struct {
	mu    sync.Mutex
	items map[string]Combined
}

// NewMemoryStore returns an empty in-memory combined-source store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{items: map[string]Combined{}} }

func (m *MemoryStore) Put(_ context.Context, c Combined) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[c.Key] = c
	return nil
}

func (m *MemoryStore) Get(_ context.Context, key string) (Combined, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.items[key]
	return c, ok, nil
}

func (m *MemoryStore) List(_ context.Context) ([]Combined, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.items))
	for k := range m.items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Combined, 0, len(keys))
	for _, k := range keys {
		out = append(out, m.items[k])
	}
	return out, nil
}

func (m *MemoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}
