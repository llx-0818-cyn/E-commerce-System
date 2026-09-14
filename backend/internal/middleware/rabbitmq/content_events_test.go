package rabbitmq

import "testing"

func TestOutboxPublisherRoutesBehaviorEventsToDedicatedExchange(t *testing.T) {
	publisher := &OutboxPublisher{
		exchange: ContentExchange,
		routes: []EventExchangeRoute{
			{EventTypePrefix: "behavior.", Exchange: BehaviorExchange},
			{EventTypePrefix: "behavior.order.", Exchange: "behavior.order.events"},
		},
	}
	for eventType, want := range map[string]string{
		"content.published":        ContentExchange,
		"behavior.content.clicked": BehaviorExchange,
		"behavior.order.created":   "behavior.order.events",
	} {
		if got := publisher.exchangeForEvent(eventType); got != want {
			t.Fatalf("exchangeForEvent(%q) = %q, want %q", eventType, got, want)
		}
	}
}
