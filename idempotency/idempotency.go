// Package idempotency provides an idempotency key store for preventing duplicate
// transaction submissions. Use this package when you need to ensure that a transaction
// request is processed only once, even if the client retries.
package idempotency

import (
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Errors
var (
	// ErrDuplicateKey is returned when a transaction with the same key already exists
	ErrDuplicateKey = fmt.Errorf("duplicate idempotency key: transaction already submitted")

	// ErrKeyNotFound is returned when looking up a non-existent key
	ErrKeyNotFound = fmt.Errorf("idempotency key not found")
)

// Status represents the status of an idempotent transaction
type Status int

const (
	StatusPending   Status = iota // Transaction is being processed
	StatusSubmitted               // Transaction has been submitted to the network
	StatusConfirmed               // Transaction has been confirmed (mined)
	StatusFailed                  // Transaction failed permanently
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusSubmitted:
		return "submitted"
	case StatusConfirmed:
		return "confirmed"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Record stores information about an idempotent transaction
type Record struct {
	Key         string
	Status      Status
	TxHash      common.Hash
	Transaction *types.Transaction
	Receipt     *types.Receipt
	Error       error
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store provides storage for idempotency keys
type Store interface {
	// Get retrieves an existing record by key
	Get(key string) (*Record, error)

	// Create creates a new record, returning error if key already exists
	Create(key string) (*Record, error)

	// Update updates an existing record
	Update(record *Record) error

	// Delete removes a record by key
	Delete(key string) error
}

// InMemoryStore is a simple in-memory implementation of Store
type InMemoryStore struct {
	mu      sync.RWMutex
	records map[string]*Record

	// TTL for records (0 means no expiration)
	ttl time.Duration

	// stopChan is used to signal the cleanup goroutine to stop
	stopChan chan struct{}
	// stopped indicates if the store has been stopped
	stopped bool
}

// NewInMemoryStore creates a new in-memory idempotency store
func NewInMemoryStore(ttl time.Duration) *InMemoryStore {
	store := &InMemoryStore{
		records:  make(map[string]*Record),
		ttl:      ttl,
		stopChan: make(chan struct{}),
	}

	// Start cleanup goroutine if TTL is set
	if ttl > 0 {
		go store.cleanupLoop()
	}

	return store
}

// Stop stops the cleanup goroutine. Should be called when the store is no longer needed
// to prevent goroutine leaks.
func (s *InMemoryStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.stopChan)
	}
}

// Get retrieves an existing record by key
func (s *InMemoryStore) Get(key string) (*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, exists := s.records[key]
	if !exists {
		return nil, ErrKeyNotFound
	}

	// Check if record has expired
	if s.ttl > 0 && time.Since(record.CreatedAt) > s.ttl {
		return nil, ErrKeyNotFound
	}

	return record, nil
}

// Create creates a new record, returning error if key already exists
func (s *InMemoryStore) Create(key string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if key already exists and is not expired
	if existing, exists := s.records[key]; exists {
		if s.ttl == 0 || time.Since(existing.CreatedAt) <= s.ttl {
			return existing, ErrDuplicateKey
		}
		// Record expired, allow overwrite
	}

	now := time.Now()
	record := &Record{
		Key:       key,
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.records[key] = record
	return record, nil
}

// Update updates an existing record
func (s *InMemoryStore) Update(record *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.records[record.Key]; !exists {
		return ErrKeyNotFound
	}

	record.UpdatedAt = time.Now()
	s.records[record.Key] = record
	return nil
}

// Delete removes a record by key
func (s *InMemoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.records, key)
	return nil
}

// cleanupLoop periodically removes expired records
func (s *InMemoryStore) cleanupLoop() {
	ticker := time.NewTicker(s.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

// cleanup removes expired records
func (s *InMemoryStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for key, record := range s.records {
		if now.Sub(record.CreatedAt) > s.ttl {
			delete(s.records, key)
		}
	}
}

// Size returns the number of records in the store
func (s *InMemoryStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}
