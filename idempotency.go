package walletarmy

import (
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	// ErrDuplicateIdempotencyKey is returned when a transaction with the same key already exists
	ErrDuplicateIdempotencyKey = fmt.Errorf("duplicate idempotency key: transaction already submitted")

	// ErrIdempotencyKeyNotFound is returned when looking up a non-existent key
	ErrIdempotencyKeyNotFound = fmt.Errorf("idempotency key not found")
)

// IdempotencyStatus represents the status of an idempotent transaction
type IdempotencyStatus int

const (
	IdempotencyStatusPending   IdempotencyStatus = iota // Transaction is being processed
	IdempotencyStatusSubmitted                          // Transaction has been submitted to the network
	IdempotencyStatusConfirmed                          // Transaction has been confirmed (mined)
	IdempotencyStatusFailed                             // Transaction failed permanently
)

func (s IdempotencyStatus) String() string {
	switch s {
	case IdempotencyStatusPending:
		return "pending"
	case IdempotencyStatusSubmitted:
		return "submitted"
	case IdempotencyStatusConfirmed:
		return "confirmed"
	case IdempotencyStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// IdempotencyRecord stores information about an idempotent transaction
type IdempotencyRecord struct {
	Key         string
	Status      IdempotencyStatus
	TxHash      common.Hash
	Transaction *types.Transaction
	Receipt     *types.Receipt
	Error       error
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IdempotencyStore provides storage for idempotency keys
type IdempotencyStore interface {
	// Get retrieves an existing record by key
	Get(key string) (*IdempotencyRecord, error)

	// Create creates a new record, returning error if key already exists
	Create(key string) (*IdempotencyRecord, error)

	// Update updates an existing record
	Update(record *IdempotencyRecord) error

	// Delete removes a record by key
	Delete(key string) error
}

// InMemoryIdempotencyStore is a simple in-memory implementation of IdempotencyStore
type InMemoryIdempotencyStore struct {
	mu      sync.RWMutex
	records map[string]*IdempotencyRecord

	// TTL for records (0 means no expiration)
	ttl time.Duration

	// stopChan is used to signal the cleanup goroutine to stop
	stopChan chan struct{}
	// stopped indicates if the store has been stopped
	stopped bool
}

// NewInMemoryIdempotencyStore creates a new in-memory idempotency store
func NewInMemoryIdempotencyStore(ttl time.Duration) *InMemoryIdempotencyStore {
	store := &InMemoryIdempotencyStore{
		records:  make(map[string]*IdempotencyRecord),
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
func (s *InMemoryIdempotencyStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.stopChan)
	}
}

// Get retrieves an existing record by key
func (s *InMemoryIdempotencyStore) Get(key string) (*IdempotencyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, exists := s.records[key]
	if !exists {
		return nil, ErrIdempotencyKeyNotFound
	}

	// Check if record has expired
	if s.ttl > 0 && time.Since(record.CreatedAt) > s.ttl {
		return nil, ErrIdempotencyKeyNotFound
	}

	return record, nil
}

// Create creates a new record, returning error if key already exists
func (s *InMemoryIdempotencyStore) Create(key string) (*IdempotencyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if key already exists and is not expired
	if existing, exists := s.records[key]; exists {
		if s.ttl == 0 || time.Since(existing.CreatedAt) <= s.ttl {
			return existing, ErrDuplicateIdempotencyKey
		}
		// Record expired, allow overwrite
	}

	now := time.Now()
	record := &IdempotencyRecord{
		Key:       key,
		Status:    IdempotencyStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.records[key] = record
	return record, nil
}

// Update updates an existing record
func (s *InMemoryIdempotencyStore) Update(record *IdempotencyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.records[record.Key]; !exists {
		return ErrIdempotencyKeyNotFound
	}

	record.UpdatedAt = time.Now()
	s.records[record.Key] = record
	return nil
}

// Delete removes a record by key
func (s *InMemoryIdempotencyStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.records, key)
	return nil
}

// cleanupLoop periodically removes expired records
func (s *InMemoryIdempotencyStore) cleanupLoop() {
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
func (s *InMemoryIdempotencyStore) cleanup() {
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
func (s *InMemoryIdempotencyStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}
