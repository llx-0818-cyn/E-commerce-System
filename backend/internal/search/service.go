package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"feedsystem_video_go/internal/config"
)

var ErrInvalidQuery = errors.New("invalid search query")
var ErrEnhancedSearchRequired = errors.New("enhanced search requires Elasticsearch")

type Service struct {
	repository *Repository
	adapter    Adapter
	enhanced   bool
}

type Adapter interface {
	Search(context.Context, Query) ([]Hit, error)
}

func NewService(repository *Repository, searchConfig config.SearchConfig) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("search service repository is nil")
	}
	provider := strings.ToLower(strings.TrimSpace(searchConfig.Provider))
	var adapter Adapter = repository
	if provider == "elasticsearch" {
		elasticsearch, err := NewElasticsearchAdapter(searchConfig.Elasticsearch)
		if err != nil {
			return nil, err
		}
		adapter = elasticsearch
		provider = "elasticsearch"
	} else if provider != "mysql" {
		return nil, fmt.Errorf("search provider %q is not available", searchConfig.Provider)
	}
	return &Service{repository: repository, adapter: adapter, enhanced: provider == "elasticsearch"}, nil
}

func (s *Service) Search(ctx context.Context, query Query) (Page, error) {
	query.Keyword = strings.TrimSpace(query.Keyword)
	if query.Keyword == "" || utf8.RuneCountInString(query.Keyword) > 100 {
		return Page{}, fmt.Errorf("%w: q must contain 1..100 characters", ErrInvalidQuery)
	}
	if query.Scope == "" {
		query.Scope = ScopeContent
	}
	if query.Scope != ScopeContent && query.Scope != ScopeProduct && query.Scope != ScopeShop {
		return Page{}, fmt.Errorf("%w: scope must be content, product or shop", ErrInvalidQuery)
	}
	if query.Limit == 0 {
		query.Limit = 20
	}
	if query.Limit < 1 || query.Limit > 50 {
		return Page{}, fmt.Errorf("%w: limit must be in 1..50", ErrInvalidQuery)
	}
	if query.Offset < 0 || query.Offset > 10_000 {
		return Page{}, fmt.Errorf("%w: offset must be in 0..10000", ErrInvalidQuery)
	}
	if query.Sort == "" {
		query.Sort = SortRelevance
	}
	if query.Sort != SortRelevance && query.Sort != SortSales && query.Sort != SortPriceAsc && query.Sort != SortPriceDesc {
		return Page{}, fmt.Errorf("%w: sort is invalid", ErrInvalidQuery)
	}
	if query.CategoryID != nil && *query.CategoryID == 0 {
		return Page{}, fmt.Errorf("%w: category_id must be positive", ErrInvalidQuery)
	}
	if (query.MinPriceCent != nil && *query.MinPriceCent < 0) || (query.MaxPriceCent != nil && *query.MaxPriceCent < 0) ||
		(query.MinPriceCent != nil && query.MaxPriceCent != nil && *query.MinPriceCent > *query.MaxPriceCent) {
		return Page{}, fmt.Errorf("%w: price range is invalid", ErrInvalidQuery)
	}
	advanced := query.CategoryID != nil || query.MinPriceCent != nil || query.MaxPriceCent != nil || query.Sort != SortRelevance
	if advanced && query.Scope != ScopeProduct {
		return Page{}, fmt.Errorf("%w: advanced filters apply only to products", ErrInvalidQuery)
	}
	if advanced && !s.enhanced {
		return Page{}, ErrEnhancedSearchRequired
	}
	loadQuery := query
	loadQuery.Limit++
	hits, err := s.adapter.Search(ctx, loadQuery)
	if err != nil {
		return Page{}, err
	}
	hasMore := len(hits) > query.Limit
	if hasMore {
		hits = hits[:query.Limit]
	}
	page := Page{Query: query.Keyword, Scope: query.Scope, Items: hits, HasMore: hasMore}
	if hasMore {
		next := query.Offset + len(hits)
		page.NextOffset = &next
	}
	return page, nil
}

func (s *Service) Elasticsearch() (*ElasticsearchAdapter, bool) {
	adapter, ok := s.adapter.(*ElasticsearchAdapter)
	return adapter, ok
}
