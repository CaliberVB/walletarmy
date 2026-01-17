// Package circuitbreaker provides a simple circuit breaker implementation
// for protecting RPC node calls from cascading failures.
package circuitbreaker

import (
	"sync"
	"time"
)

// State represents the circuit breaker state
type State int

const (
	StateClosed   State = iota // Normal operation, requests pass through
	StateOpen                  // Circuit is open, requests fail fast
	StateHalfOpen              // Testing if the circuit can be closed
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config holds the configuration for a circuit breaker
type Config struct {
	// FailureThreshold is the number of consecutive failures before opening the circuit
	FailureThreshold int

	// SuccessThreshold is the number of consecutive successes in half-open state
	// before closing the circuit
	SuccessThreshold int

	// Timeout is how long the circuit stays open before moving to half-open
	Timeout time.Duration

	// OnStateChange is called when the circuit breaker state changes
	OnStateChange func(from, to State)
}

// DefaultConfig returns a default circuit breaker configuration
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          30 * time.Second,
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	mu sync.RWMutex

	config Config
	state  State

	// Counters
	consecutiveFailures  int
	consecutiveSuccesses int

	// Timing
	lastFailureTime time.Time
}

// New creates a new circuit breaker with the given configuration
func New(config Config) *CircuitBreaker {
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 5
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 2
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}

	return &CircuitBreaker{
		config: config,
		state:  StateClosed,
	}
}

// State returns the current state of the circuit breaker
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.currentState()
}

// currentState returns the current state, potentially transitioning from open to half-open
// Must be called with at least a read lock held
func (cb *CircuitBreaker) currentState() State {
	if cb.state == StateOpen {
		if time.Since(cb.lastFailureTime) >= cb.config.Timeout {
			return StateHalfOpen
		}
	}
	return cb.state
}

// Allow returns true if a request should be allowed through
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	state := cb.currentState()

	switch state {
	case StateClosed:
		return true
	case StateOpen:
		return false
	case StateHalfOpen:
		// Allow one request through to test the circuit
		return true
	default:
		return true
	}
}

// RecordSuccess records a successful operation
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures = 0
	cb.consecutiveSuccesses++

	state := cb.currentState()

	if state == StateHalfOpen {
		if cb.consecutiveSuccesses >= cb.config.SuccessThreshold {
			cb.setState(StateClosed)
			cb.consecutiveSuccesses = 0
		}
	}
}

// RecordFailure records a failed operation
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveSuccesses = 0
	cb.consecutiveFailures++
	cb.lastFailureTime = time.Now()

	state := cb.currentState()

	switch state {
	case StateClosed:
		if cb.consecutiveFailures >= cb.config.FailureThreshold {
			cb.setState(StateOpen)
		}
	case StateHalfOpen:
		// Any failure in half-open state reopens the circuit
		cb.setState(StateOpen)
	}
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures = 0
	cb.consecutiveSuccesses = 0
	cb.setState(StateClosed)
}

// setState changes the state and calls the callback if configured
func (cb *CircuitBreaker) setState(newState State) {
	if cb.state == newState {
		return
	}

	oldState := cb.state
	cb.state = newState

	if cb.config.OnStateChange != nil {
		// Call in a goroutine to avoid blocking
		go cb.config.OnStateChange(oldState, newState)
	}
}

// Stats returns current circuit breaker statistics
type Stats struct {
	State                State
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastFailureTime      time.Time
}

// Stats returns the current statistics
func (cb *CircuitBreaker) Stats() Stats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return Stats{
		State:                cb.currentState(),
		ConsecutiveFailures:  cb.consecutiveFailures,
		ConsecutiveSuccesses: cb.consecutiveSuccesses,
		LastFailureTime:      cb.lastFailureTime,
	}
}
