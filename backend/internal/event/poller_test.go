package event

import (
	"errors"
	"testing"
	"time"
)

func TestBackoffForAttemptIsBoundedExponential(t *testing.T) {
	base := 100 * time.Millisecond
	maximum := 750 * time.Millisecond
	tests := []struct {
		attempt uint
		want    time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 750 * time.Millisecond},
		{10, 750 * time.Millisecond},
	}
	for _, test := range tests {
		if got := backoffForAttempt(test.attempt, base, maximum); got != test.want {
			t.Fatalf("backoffForAttempt(%d) = %s, want %s", test.attempt, got, test.want)
		}
	}
}

func TestPollerConfigValidation(t *testing.T) {
	config := DefaultPollerConfig("poller-a")
	if err := config.Validate(); err != nil {
		t.Fatalf("default config Validate() error = %v", err)
	}
	config.BatchSize = 0
	if err := config.Validate(); err == nil {
		t.Fatal("zero batch size accepted")
	}
}

func TestTruncateErrorPreservesUTF8(t *testing.T) {
	if got := truncateError(errors.New("失败原因过长"), 4); got != "失败原因" {
		t.Fatalf("truncateError() = %q", got)
	}
}
