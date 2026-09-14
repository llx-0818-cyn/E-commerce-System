package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"feedsystem_video_go/internal/config"
)

var ErrIndexMissing = errors.New("search index is missing")

type ElasticsearchAdapter struct {
	addresses []string
	username  string
	password  string
	alias     string
	client    *http.Client
	next      atomic.Uint64
}

func NewElasticsearchAdapter(cfg config.ElasticsearchConfig) (*ElasticsearchAdapter, error) {
	if len(cfg.Addresses) == 0 || strings.TrimSpace(cfg.IndexPrefix) == "" || cfg.RequestTimeout.Value() <= 0 {
		return nil, errors.New("elasticsearch search configuration is incomplete")
	}
	addresses := make([]string, 0, len(cfg.Addresses))
	for _, address := range cfg.Addresses {
		parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(address), "/"))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("invalid Elasticsearch address %q", address)
		}
		addresses = append(addresses, parsed.String())
	}
	return &ElasticsearchAdapter{
		addresses: addresses, username: cfg.Username, password: cfg.Password,
		alias:  cfg.IndexPrefix + "_commerce_search",
		client: &http.Client{Timeout: cfg.RequestTimeout.Value()},
	}, nil
}

func (a *ElasticsearchAdapter) Search(ctx context.Context, query Query) ([]Hit, error) {
	filters := []any{map[string]any{"term": map[string]any{"type": query.Scope}}}
	if query.CategoryID != nil {
		filters = append(filters, map[string]any{"term": map[string]any{"category_id": *query.CategoryID}})
	}
	priceRange := map[string]any{}
	if query.MinPriceCent != nil {
		priceRange["gte"] = *query.MinPriceCent
	}
	if query.MaxPriceCent != nil {
		priceRange["lte"] = *query.MaxPriceCent
	}
	if len(priceRange) > 0 {
		filters = append(filters, map[string]any{"range": map[string]any{"price_cent": priceRange}})
	}
	sortFields := []any{map[string]any{"_score": "desc"}, map[string]any{"id": "desc"}}
	switch query.Sort {
	case SortSales:
		sortFields = []any{map[string]any{"sales_count": "desc"}, map[string]any{"_score": "desc"}, map[string]any{"id": "desc"}}
	case SortPriceAsc:
		sortFields = []any{map[string]any{"price_cent": map[string]any{"order": "asc", "missing": "_last"}}, map[string]any{"id": "desc"}}
	case SortPriceDesc:
		sortFields = []any{map[string]any{"price_cent": map[string]any{"order": "desc", "missing": "_last"}}, map[string]any{"id": "desc"}}
	}
	body := map[string]any{
		"from": query.Offset,
		"size": query.Limit,
		"query": map[string]any{"bool": map[string]any{
			"filter": filters,
			"must": []any{map[string]any{"multi_match": map[string]any{
				"query": query.Keyword, "fields": []string{"title^4", "subtitle^2", "body", "tags^3"},
			}}},
		}},
		"sort": sortFields,
	}
	responseBody, err := a.doJSON(ctx, http.MethodPost, "/"+url.PathEscape(a.alias)+"/_search", body, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var response struct {
		Hits struct {
			Hits []struct {
				Source Document `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode Elasticsearch search response: %w", err)
	}
	hits := make([]Hit, 0, len(response.Hits.Hits))
	for _, item := range response.Hits.Hits {
		document := item.Source
		subtitle := document.Subtitle
		if subtitle == "" && document.Type == ScopeContent {
			subtitle = document.Body
		}
		hits = append(hits, Hit{
			Type: document.Type, ID: document.ID, Title: document.Title, Subtitle: subtitle,
			ImageURL: document.ImageURL, PriceCent: document.PriceCent, PublishedAt: document.PublishedAt,
		})
	}
	return hits, nil
}

func (a *ElasticsearchAdapter) Upsert(ctx context.Context, document Document) error {
	if !validDocument(document) {
		return errors.New("search document is invalid")
	}
	_, err := a.doJSON(ctx, http.MethodPut,
		"/"+url.PathEscape(a.alias)+"/_doc/"+url.PathEscape(documentID(document.Type, document.ID))+"?refresh=false",
		document, http.StatusOK, http.StatusCreated)
	return err
}

func (a *ElasticsearchAdapter) Delete(ctx context.Context, scope Scope, id uint64) error {
	if id == 0 || (scope != ScopeContent && scope != ScopeProduct && scope != ScopeShop) {
		return errors.New("search document identity is invalid")
	}
	_, err := a.doJSON(ctx, http.MethodDelete,
		"/"+url.PathEscape(a.alias)+"/_doc/"+url.PathEscape(documentID(scope, id))+"?refresh=false",
		nil, http.StatusOK, http.StatusNotFound)
	if errors.Is(err, ErrIndexMissing) {
		return err
	}
	return err
}

func (a *ElasticsearchAdapter) Rebuild(ctx context.Context, source DocumentSource) (string, error) {
	if source == nil {
		return "", errors.New("search rebuild source is nil")
	}
	index := fmt.Sprintf("%s_%s_%d", a.alias, time.Now().UTC().Format("20060102150405"), time.Now().UnixNano())
	if _, err := a.doJSON(ctx, http.MethodPut, "/"+url.PathEscape(index), indexDefinition(), http.StatusOK); err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_, _ = a.doJSON(context.Background(), http.MethodDelete, "/"+url.PathEscape(index), nil, http.StatusOK, http.StatusNotFound)
		}
	}()
	for _, scope := range []Scope{ScopeContent, ScopeProduct, ScopeShop} {
		var afterID uint64
		for {
			documents, err := source.ListDocuments(ctx, scope, afterID, 500)
			if err != nil {
				return "", err
			}
			if len(documents) == 0 {
				break
			}
			if err := a.bulkIndex(ctx, index, documents); err != nil {
				return "", err
			}
			afterID = documents[len(documents)-1].ID
			if len(documents) < 500 {
				break
			}
		}
	}
	if _, err := a.doJSON(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_refresh", nil, http.StatusOK); err != nil {
		return "", err
	}
	actions := map[string]any{"actions": []any{
		map[string]any{"remove": map[string]any{"index": "*", "alias": a.alias, "must_exist": false}},
		map[string]any{"add": map[string]any{"index": index, "alias": a.alias}},
	}}
	if _, err := a.doJSON(ctx, http.MethodPost, "/_aliases", actions, http.StatusOK); err != nil {
		return "", err
	}
	cleanup = false
	return index, nil
}

func (a *ElasticsearchAdapter) bulkIndex(ctx context.Context, index string, documents []Document) error {
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	for _, document := range documents {
		if !validDocument(document) {
			return errors.New("search rebuild contains invalid document")
		}
		if err := encoder.Encode(map[string]any{"index": map[string]any{"_id": documentID(document.Type, document.ID)}}); err != nil {
			return err
		}
		if err := encoder.Encode(document); err != nil {
			return err
		}
	}
	responseBody, err := a.do(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_bulk?refresh=false", payload.Bytes(), "application/x-ndjson", http.StatusOK)
	if err != nil {
		return err
	}
	var response struct {
		Errors bool `json:"errors"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return fmt.Errorf("decode Elasticsearch bulk response: %w", err)
	}
	if response.Errors {
		return errors.New("Elasticsearch bulk indexing reported item failures")
	}
	return nil
}

func (a *ElasticsearchAdapter) doJSON(ctx context.Context, method, path string, value any, statuses ...int) ([]byte, error) {
	var payload []byte
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		payload = encoded
	}
	return a.do(ctx, method, path, payload, "application/json", statuses...)
}

func (a *ElasticsearchAdapter) do(ctx context.Context, method, path string, payload []byte, contentType string, statuses ...int) ([]byte, error) {
	if a == nil || len(a.addresses) == 0 || a.client == nil {
		return nil, errors.New("Elasticsearch adapter is not initialized")
	}
	address := a.addresses[int(a.next.Add(1)-1)%len(a.addresses)]
	request, err := http.NewRequestWithContext(ctx, method, address+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if len(payload) > 0 {
		request.Header.Set("Content-Type", contentType)
	}
	if a.username != "" {
		request.SetBasicAuth(a.username, a.password)
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Elasticsearch %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	for _, status := range statuses {
		if response.StatusCode == status {
			if response.StatusCode == http.StatusNotFound && bytes.Contains(body, []byte("index_not_found_exception")) {
				return nil, ErrIndexMissing
			}
			return body, nil
		}
	}
	if response.StatusCode == http.StatusNotFound && bytes.Contains(body, []byte("index_not_found_exception")) {
		return nil, ErrIndexMissing
	}
	return nil, fmt.Errorf("Elasticsearch %s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(body)))
}

func indexDefinition() map[string]any {
	return map[string]any{
		"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0},
		"mappings": map[string]any{
			"dynamic": "strict",
			"properties": map[string]any{
				"type": map[string]any{"type": "keyword"}, "id": map[string]any{"type": "long"},
				"title": map[string]any{"type": "text"}, "subtitle": map[string]any{"type": "text"},
				"body": map[string]any{"type": "text"}, "tags": map[string]any{"type": "text"},
				"image_url":  map[string]any{"type": "keyword", "index": false},
				"price_cent": map[string]any{"type": "long"}, "category_id": map[string]any{"type": "long"},
				"sales_count": map[string]any{"type": "long"}, "published_at": map[string]any{"type": "date"},
			},
		},
	}
}

func documentID(scope Scope, id uint64) string {
	return fmt.Sprintf("%s:%d", scope, id)
}

func validDocument(document Document) bool {
	return document.ID > 0 && document.Title != "" && (document.Type == ScopeContent || document.Type == ScopeProduct || document.Type == ScopeShop)
}

var _ Adapter = (*ElasticsearchAdapter)(nil)
var _ IndexWriter = (*ElasticsearchAdapter)(nil)
