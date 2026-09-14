package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	domainevent "feedsystem_video_go/internal/event"
)

const IndexRequestedEvent = "search.index.requested"

type IndexAction string

const (
	IndexUpsert IndexAction = "upsert"
	IndexDelete IndexAction = "delete"
)

type IndexRequest struct {
	Scope  Scope       `json:"scope"`
	ID     uint64      `json:"id"`
	Action IndexAction `json:"action"`
}

func NewIndexEvent(request IndexRequest) (domainevent.DomainEvent, error) {
	if !validIndexRequest(request) {
		return domainevent.DomainEvent{}, errors.New("search index request is invalid")
	}
	return domainevent.DomainEvent{
		AggregateType: "search_document",
		AggregateID:   string(request.Scope) + ":" + strconv.FormatUint(request.ID, 10),
		EventType:     IndexRequestedEvent,
		Payload:       request,
	}, nil
}

func AppendIndexRequest(ctx context.Context, events *domainevent.Repository, request IndexRequest) error {
	if events == nil {
		return errors.New("search index event repository is nil")
	}
	event, err := NewIndexEvent(request)
	if err != nil {
		return err
	}
	_, err = events.AppendStandalone(ctx, event)
	return err
}

type IndexWriter interface {
	Upsert(context.Context, Document) error
	Delete(context.Context, Scope, uint64) error
}

type IndexConsumer struct {
	source DocumentSource
	index  IndexWriter
}

func NewIndexConsumer(source DocumentSource, index IndexWriter) (*IndexConsumer, error) {
	if source == nil || index == nil {
		return nil, errors.New("search index consumer dependencies are required")
	}
	return &IndexConsumer{source: source, index: index}, nil
}

func (c *IndexConsumer) Process(ctx context.Context, eventType string, payloadJSON []byte) error {
	if c == nil || c.source == nil || c.index == nil {
		return errors.New("search index consumer is not initialized")
	}
	if eventType != IndexRequestedEvent {
		return fmt.Errorf("unsupported search event %q", eventType)
	}
	var request IndexRequest
	if err := json.Unmarshal(payloadJSON, &request); err != nil || !validIndexRequest(request) {
		return errors.New("search index event payload is invalid")
	}
	if request.Action == IndexDelete {
		return c.index.Delete(ctx, request.Scope, request.ID)
	}
	document, found, err := c.source.LoadDocument(ctx, request.Scope, request.ID)
	if err != nil {
		return err
	}
	if !found {
		return c.index.Delete(ctx, request.Scope, request.ID)
	}
	return c.index.Upsert(ctx, *document)
}

func validIndexRequest(request IndexRequest) bool {
	return request.ID > 0 && (request.Scope == ScopeContent || request.Scope == ScopeProduct || request.Scope == ScopeShop) &&
		(request.Action == IndexUpsert || request.Action == IndexDelete)
}
