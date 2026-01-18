package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		cb := New(DefaultConfig())
		if cb.State() != StateClosed {
			t.Errorf("expected initial state to be Closed, got %v", cb.State())
		}
	})

	t.Run("custom config", func(t *testing.T) {
		config := Config{
			FailureThreshold: 3,
			SuccessThreshold: 1,
			Timeout:          10 * time.Second,
		}
		cb := New(config)
		if cb.config.FailureThreshold != 3 {
			t.Errorf("expected FailureThreshold 3, got %d", cb.config.FailureThreshold)
		}
	})

	t.Run("invalid config values corrected", func(t *testing.T) {
		config := Config{
			FailureThreshold: 0,
			SuccessThreshold: -1,
			Timeout:          0,
		}
		cb := New(config)
		if cb.config.FailureThreshold != 5 {
			t.Errorf("expected default FailureThreshold 5, got %d", cb.config.FailureThreshold)
		}
		if cb.config.SuccessThreshold != 2 {
			t.Errorf("expected default SuccessThreshold 2, got %d", cb.config.SuccessThreshold)
		}
		if cb.config.Timeout != 30*time.Second {
			t.Errorf("expected default Timeout 30s, got %v", cb.config.Timeout)
		}
	})
}

func TestStateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.expected {
			t.Errorf("State(%d).String() = %q, expected %q", tt.state, got, tt.expected)
		}
	}
}

func TestAllow(t *testing.T) {
	t.Run("closed state allows requests", func(t *testing.T) {
		cb := New(DefaultConfig())
		if !cb.Allow() {
			t.Error("expected Allow() to return true in closed state")
		}
	})

	t.Run("open state blocks requests", func(t *testing.T) {
		cb := New(Config{
			FailureThreshold: 2,
			SuccessThreshold: 1,
			Timeout:          1 * time.Hour, // Long timeout so it stays open
		})

		// Trigger failures to open circuit
		cb.RecordFailure()
		cb.RecordFailure()

		if cb.Allow() {
			t.Error("expected Allow() to return false in open state")
		}
	})

	t.Run("half-open state allows requests", func(t *testing.T) {
		cb := New(Config{
			FailureThreshold: 1,
			SuccessThreshold: 1,
			Timeout:          1 * time.Millisecond,
		})

		// Open the circuit
		cb.RecordFailure()

		// Wait for timeout to transition to half-open
		time.Sleep(5 * time.Millisecond)

		if !cb.Allow() {
			t.Error("expected Allow() to return true in half-open state")
		}
	})
}

func TestRecordSuccess(t *testing.T) {
	t.Run("resets failure count", func(t *testing.T) {
		cb := New(Config{
			FailureThreshold: 3,
			SuccessThreshold: 1,
			Timeout:          30 * time.Second,
		})

		cb.RecordFailure()
		cb.RecordFailure()
		cb.RecordSuccess()

		stats := cb.Stats()
		if stats.ConsecutiveFailures != 0 {
			t.Errorf("expected ConsecutiveFailures to be 0 after success, got %d", stats.ConsecutiveFailures)
		}
	})

	t.Run("closes circuit in half-open state after threshold", func(t *testing.T) {
		cb := New(Config{
			FailureThreshold: 1,
			SuccessThreshold: 2,
			Timeout:          1 * time.Millisecond,
		})

		// Open the circuit
		cb.RecordFailure()

		// Wait for half-open
		time.Sleep(5 * time.Millisecond)

		// Record successes to close
		cb.RecordSuccess()
		if cb.State() != StateHalfOpen {
			t.Error("expected state to still be half-open after 1 success")
		}

		cb.RecordSuccess()
		if cb.State() != StateClosed {
			t.Errorf("expected state to be closed after 2 successes, got %v", cb.State())
		}
	})
}

func TestRecordFailure(t *testing.T) {
	t.Run("opens circuit after threshold", func(t *testing.T) {
		cb := New(Config{
			FailureThreshold: 3,
			SuccessThreshold: 1,
			Timeout:          30 * time.Second,
		})

		cb.RecordFailure()
		cb.RecordFailure()
		if cb.State() != StateClosed {
			t.Error("expected state to be closed before threshold")
		}

		cb.RecordFailure()
		if cb.State() != StateOpen {
			t.Errorf("expected state to be open after threshold, got %v", cb.State())
		}
	})

	t.Run("reopens circuit on failure in half-open state", func(t *testing.T) {
		cb := New(Config{
			FailureThreshold: 1,
			SuccessThreshold: 2,
			Timeout:          1 * time.Millisecond,
		})

		// Open the circuit
		cb.RecordFailure()

		// Wait for half-open
		time.Sleep(5 * time.Millisecond)

		// Verify half-open
		if cb.State() != StateHalfOpen {
			t.Fatalf("expected half-open state, got %v", cb.State())
		}

		// Record a failure, should reopen
		cb.RecordFailure()

		if cb.State() != StateOpen {
			t.Errorf("expected state to be open after failure in half-open, got %v", cb.State())
		}
	})

	t.Run("resets success count", func(t *testing.T) {
		cb := New(DefaultConfig())

		cb.RecordSuccess()
		cb.RecordSuccess()
		cb.RecordFailure()

		stats := cb.Stats()
		if stats.ConsecutiveSuccesses != 0 {
			t.Errorf("expected ConsecutiveSuccesses to be 0 after failure, got %d", stats.ConsecutiveSuccesses)
		}
	})
}

func TestReset(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          1 * time.Hour,
	})

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatal("expected circuit to be open")
	}

	cb.Reset()

	if cb.State() != StateClosed {
		t.Errorf("expected state to be closed after reset, got %v", cb.State())
	}

	stats := cb.Stats()
	if stats.ConsecutiveFailures != 0 {
		t.Errorf("expected ConsecutiveFailures to be 0 after reset, got %d", stats.ConsecutiveFailures)
	}
	if stats.ConsecutiveSuccesses != 0 {
		t.Errorf("expected ConsecutiveSuccesses to be 0 after reset, got %d", stats.ConsecutiveSuccesses)
	}
}

func TestOnStateChange(t *testing.T) {
	var (
		mu          sync.Mutex
		transitions []struct{ from, to State }
	)

	cb := New(Config{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          1 * time.Millisecond,
		OnStateChange: func(from, to State) {
			mu.Lock()
			transitions = append(transitions, struct{ from, to State }{from, to})
			mu.Unlock()
		},
	})

	// Open the circuit
	cb.RecordFailure()

	// Give callback time to execute (it runs in goroutine)
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].from != StateClosed || transitions[0].to != StateOpen {
		t.Errorf("expected transition Closed->Open, got %v->%v", transitions[0].from, transitions[0].to)
	}
	mu.Unlock()
}

func TestConcurrentAccess(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 100,
		SuccessThreshold: 10,
		Timeout:          30 * time.Second,
	})

	var wg sync.WaitGroup
	numGoroutines := 100
	numOperations := 100

	// Concurrent failures
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				cb.RecordFailure()
				cb.Allow()
				cb.Stats()
			}
		}()
	}

	// Concurrent successes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				cb.RecordSuccess()
				cb.Allow()
				cb.State()
			}
		}()
	}

	// Concurrent resets
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				cb.Reset()
			}
		}()
	}

	wg.Wait()

	// If we got here without a race detector complaint, the test passes
}

func TestStats(t *testing.T) {
	cb := New(DefaultConfig())

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()
	cb.RecordSuccess()
	cb.RecordSuccess()

	stats := cb.Stats()

	if stats.State != StateClosed {
		t.Errorf("expected state Closed, got %v", stats.State)
	}
	if stats.ConsecutiveFailures != 0 {
		t.Errorf("expected ConsecutiveFailures 0, got %d", stats.ConsecutiveFailures)
	}
	if stats.ConsecutiveSuccesses != 3 {
		t.Errorf("expected ConsecutiveSuccesses 3, got %d", stats.ConsecutiveSuccesses)
	}
}

func TestHalfOpenTimeout(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          50 * time.Millisecond,
	})

	// Open the circuit
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatal("expected circuit to be open")
	}

	// Should still be open before timeout
	time.Sleep(20 * time.Millisecond)
	if cb.State() != StateOpen {
		t.Error("expected circuit to still be open before timeout")
	}

	// Should transition to half-open after timeout
	time.Sleep(40 * time.Millisecond)
	if cb.State() != StateHalfOpen {
		t.Errorf("expected circuit to be half-open after timeout, got %v", cb.State())
	}
}
