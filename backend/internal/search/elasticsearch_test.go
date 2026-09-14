package search

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
)

func TestElasticsearchMissingIndexCanRebuildAndAtomicallySwitchAlias(t *testing.T) {
	fake := &fakeElasticsearch{documents: make(map[string]Document)}
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	defer server.Close()
	adapter, err := NewElasticsearchAdapter(config.ElasticsearchConfig{
		Addresses: []string{server.URL}, IndexPrefix: "feedsystem_test", RequestTimeout: config.Duration(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("NewElasticsearchAdapter() error = %v", err)
	}
	if _, err := adapter.Search(context.Background(), Query{Keyword: "内容", Scope: ScopeContent, Limit: 10}); !errors.Is(err, ErrIndexMissing) {
		t.Fatalf("Search() before rebuild error = %v, want missing index", err)
	}
	source := &fakeDocumentSource{documents: []Document{
		{Type: ScopeContent, ID: 1, Title: "索引内容", Body: "正文", Tags: []string{"标签"}},
		{Type: ScopeProduct, ID: 2, Title: "索引商品", SalesCount: 3},
		{Type: ScopeShop, ID: 3, Title: "索引店铺"},
	}}
	firstIndex, err := adapter.Rebuild(context.Background(), source)
	if err != nil {
		t.Fatalf("first Rebuild() error = %v", err)
	}
	secondIndex, err := adapter.Rebuild(context.Background(), source)
	if err != nil {
		t.Fatalf("second Rebuild() error = %v", err)
	}
	if firstIndex == secondIndex {
		t.Fatalf("repeated rebuild reused physical index %q", firstIndex)
	}
	fake.mu.Lock()
	aliasSwitches, mappedFields, indexed := fake.aliasSwitches, fake.mappedFields, len(fake.documents)
	fake.mu.Unlock()
	if aliasSwitches != 2 || !mappedFields["type"] || !mappedFields["title"] || !mappedFields["tags"] || !mappedFields["category_id"] || !mappedFields["sales_count"] {
		t.Fatalf("alias switches/mapping = %d/%v", aliasSwitches, mappedFields)
	}
	if indexed != 3 {
		t.Fatalf("indexed documents = %d, want 3", indexed)
	}
	hits, err := adapter.Search(context.Background(), Query{Keyword: "索引", Scope: ScopeContent, Limit: 10})
	if err != nil || len(hits) == 0 || hits[0].ID != 1 || hits[0].Type != ScopeContent {
		t.Fatalf("Search() after rebuild = %#v, %v", hits, err)
	}
	categoryID, minPrice, maxPrice := uint64(7), int64(1000), int64(9000)
	if _, err := adapter.Search(context.Background(), Query{
		Keyword: "商品", Scope: ScopeProduct, Limit: 20, CategoryID: &categoryID,
		MinPriceCent: &minPrice, MaxPriceCent: &maxPrice, Sort: SortSales,
	}); err != nil {
		t.Fatalf("advanced Search() error = %v", err)
	}
	fake.mu.Lock()
	advancedJSON, _ := json.Marshal(fake.lastSearch)
	fake.mu.Unlock()
	for _, fragment := range []string{`"category_id":7`, `"price_cent":{"gte":1000,"lte":9000}`, `"sales_count":"desc"`} {
		if !strings.Contains(string(advancedJSON), fragment) {
			t.Fatalf("advanced Elasticsearch DSL missing %s: %s", fragment, advancedJSON)
		}
	}
}

func TestIndexConsumerUpsertsAndDeletesFromIncrementalEvents(t *testing.T) {
	source := &fakeDocumentSource{documents: []Document{{Type: ScopeProduct, ID: 8, Title: "增量商品"}}}
	writer := &fakeIndexWriter{}
	consumer, err := NewIndexConsumer(source, writer)
	if err != nil {
		t.Fatalf("NewIndexConsumer() error = %v", err)
	}
	upsert, _ := json.Marshal(IndexRequest{Scope: ScopeProduct, ID: 8, Action: IndexUpsert})
	if err := consumer.Process(context.Background(), IndexRequestedEvent, upsert); err != nil {
		t.Fatalf("Process(upsert) error = %v", err)
	}
	missing, _ := json.Marshal(IndexRequest{Scope: ScopeProduct, ID: 9, Action: IndexUpsert})
	if err := consumer.Process(context.Background(), IndexRequestedEvent, missing); err != nil {
		t.Fatalf("Process(missing upsert) error = %v", err)
	}
	deleted, _ := json.Marshal(IndexRequest{Scope: ScopeProduct, ID: 8, Action: IndexDelete})
	if err := consumer.Process(context.Background(), IndexRequestedEvent, deleted); err != nil {
		t.Fatalf("Process(delete) error = %v", err)
	}
	if len(writer.upserted) != 1 || writer.upserted[0].ID != 8 || len(writer.deleted) != 2 || writer.deleted[0] != "product:9" || writer.deleted[1] != "product:8" {
		t.Fatalf("incremental effects = upsert %#v delete %#v", writer.upserted, writer.deleted)
	}
}

type fakeElasticsearch struct {
	mu            sync.Mutex
	aliasReady    bool
	aliasSwitches int
	documents     map[string]Document
	mappedFields  map[string]bool
	lastSearch    map[string]any
}

func (f *fakeElasticsearch) handle(response http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	response.Header().Set("Content-Type", "application/json")
	path := request.URL.Path
	switch {
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/_search"):
		if !f.aliasReady {
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"error":{"type":"index_not_found_exception"}}`))
			return
		}
		_ = json.NewDecoder(request.Body).Decode(&f.lastSearch)
		documents := make([]Document, 0, len(f.documents))
		for _, document := range f.documents {
			if document.Type == ScopeContent {
				documents = append(documents, document)
			}
		}
		sort.Slice(documents, func(i, j int) bool { return documents[i].ID > documents[j].ID })
		hits := make([]map[string]any, 0, len(documents))
		for _, document := range documents {
			hits = append(hits, map[string]any{"_source": document})
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"hits": map[string]any{"hits": hits}})
	case request.Method == http.MethodPut && strings.Count(strings.Trim(path, "/"), "/") == 0:
		var definition struct {
			Mappings struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"mappings"`
		}
		_ = json.NewDecoder(request.Body).Decode(&definition)
		f.mappedFields = make(map[string]bool, len(definition.Mappings.Properties))
		for field := range definition.Mappings.Properties {
			f.mappedFields[field] = true
		}
		f.documents = make(map[string]Document)
		_, _ = response.Write([]byte(`{"acknowledged":true}`))
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/_bulk"):
		scanner := bufio.NewScanner(request.Body)
		for scanner.Scan() {
			var action struct {
				Index struct {
					ID string `json:"_id"`
				} `json:"index"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &action)
			if !scanner.Scan() {
				break
			}
			var document Document
			_ = json.Unmarshal(scanner.Bytes(), &document)
			f.documents[action.Index.ID] = document
		}
		_, _ = response.Write([]byte(`{"errors":false}`))
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/_refresh"):
		_, _ = response.Write([]byte(`{"_shards":{"successful":1}}`))
	case request.Method == http.MethodPost && path == "/_aliases":
		f.aliasReady = true
		f.aliasSwitches++
		_, _ = response.Write([]byte(`{"acknowledged":true}`))
	case request.Method == http.MethodDelete:
		_, _ = response.Write([]byte(`{"acknowledged":true}`))
	default:
		response.WriteHeader(http.StatusNotFound)
		_, _ = response.Write([]byte(`{"error":{"type":"not_found"}}`))
	}
}

type fakeDocumentSource struct {
	documents []Document
}

func (s *fakeDocumentSource) LoadDocument(_ context.Context, scope Scope, id uint64) (*Document, bool, error) {
	for _, document := range s.documents {
		if document.Type == scope && document.ID == id {
			copy := document
			return &copy, true, nil
		}
	}
	return nil, false, nil
}

func (s *fakeDocumentSource) ListDocuments(_ context.Context, scope Scope, afterID uint64, limit int) ([]Document, error) {
	result := make([]Document, 0, limit)
	for _, document := range s.documents {
		if document.Type == scope && document.ID > afterID {
			result = append(result, document)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

type fakeIndexWriter struct {
	upserted []Document
	deleted  []string
}

func (w *fakeIndexWriter) Upsert(_ context.Context, document Document) error {
	w.upserted = append(w.upserted, document)
	return nil
}

func (w *fakeIndexWriter) Delete(_ context.Context, scope Scope, id uint64) error {
	w.deleted = append(w.deleted, documentID(scope, id))
	return nil
}
