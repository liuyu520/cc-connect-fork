package core

import (
	"fmt"
	"testing"
	"time"
)

func TestRetryWrite_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := retryWrite(2, 0, nil, func() error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Errorf("succeed first try: err=%v, calls=%d", err, calls)
	}
}

func TestRetryWrite_FailsThenSucceeds(t *testing.T) {
	calls := 0
	err := retryWrite(2, 0, nil, func() error {
		calls++
		if calls < 2 {
			return fmt.Errorf("transient error")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Errorf("retry success: err=%v, calls=%d", err, calls)
	}
}

func TestRetryWrite_AllRetriesFail(t *testing.T) {
	calls := 0
	err := retryWrite(2, 0, nil, func() error {
		calls++
		return fmt.Errorf("persistent error")
	})
	if err == nil {
		t.Error("expected error, got nil")
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestRetryWrite_InterruptedByDone(t *testing.T) {
	done := make(chan struct{})
	close(done) // already closed — should interrupt immediately

	calls := 0
	err := retryWrite(2, 10*time.Second, done, func() error {
		calls++
		return fmt.Errorf("fail")
	})
	if err == nil {
		t.Error("expected error, got nil")
	}
	// Should have run fn once, then hit done channel during delay (not waited 10s)
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (interrupted before retry)", calls)
	}
}
