package idempotency

import (
	"sync"
	"testing"
	"time"
)

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status   Status
		expected string
	}{
		{StatusPending, "pending"},
		{StatusSubmitted, "submitted"},
		{StatusConfirmed, "confirmed"},
		{StatusFailed, "failed"},
		{Status(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.expected {
			t.Errorf("Status(%d).String() = %q, expected %q", tt.status, got, tt.expected)
		}
	}
}

func TestNewInMemoryStore(t *testing.T) {
	t.Run("creates store without TTL", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		if store == nil {
			t.Fatal("expected non-nil store")
		}
		if store.Size() != 0 {
			t.Errorf("expected empty store, got size %d", store.Size())
		}
	})

	t.Run("creates store with TTL", func(t *testing.T) {
		store := NewInMemoryStore(1 * time.Hour)
		defer store.Stop()

		if store == nil {
			t.Fatal("expected non-nil store")
		}
	})
}

func TestInMemoryStore_Create(t *testing.T) {
	t.Run("creates new record", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		record, err := store.Create("key1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if record == nil {
			t.Fatal("expected non-nil record")
		}
		if record.Key != "key1" {
			t.Errorf("expected key 'key1', got %q", record.Key)
		}
		if record.Status != StatusPending {
			t.Errorf("expected status Pending, got %v", record.Status)
		}
	})

	t.Run("returns error on duplicate key", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		_, err := store.Create("key1")
		if err != nil {
			t.Fatalf("unexpected error on first create: %v", err)
		}

		_, err = store.Create("key1")
		if err != ErrDuplicateKey {
			t.Errorf("expected ErrDuplicateKey, got %v", err)
		}
	})

	t.Run("allows recreate after TTL expiry", func(t *testing.T) {
		store := NewInMemoryStore(50 * time.Millisecond)
		defer store.Stop()

		_, err := store.Create("key1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Wait for expiry
		time.Sleep(60 * time.Millisecond)

		// Should allow create now
		_, err = store.Create("key1")
		if err != nil {
			t.Errorf("expected no error after TTL expiry, got %v", err)
		}
	})
}

func TestInMemoryStore_Get(t *testing.T) {
	t.Run("gets existing record", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		created, _ := store.Create("key1")

		got, err := store.Get("key1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Key != created.Key {
			t.Errorf("expected key %q, got %q", created.Key, got.Key)
		}
	})

	t.Run("returns error for non-existent key", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		_, err := store.Get("nonexistent")
		if err != ErrKeyNotFound {
			t.Errorf("expected ErrKeyNotFound, got %v", err)
		}
	})

	t.Run("returns error for expired record", func(t *testing.T) {
		store := NewInMemoryStore(50 * time.Millisecond)
		defer store.Stop()

		_, _ = store.Create("key1")

		// Wait for expiry
		time.Sleep(60 * time.Millisecond)

		_, err := store.Get("key1")
		if err != ErrKeyNotFound {
			t.Errorf("expected ErrKeyNotFound for expired record, got %v", err)
		}
	})
}

func TestInMemoryStore_Update(t *testing.T) {
	t.Run("updates existing record", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		record, _ := store.Create("key1")
		record.Status = StatusConfirmed

		err := store.Update(record)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, _ := store.Get("key1")
		if got.Status != StatusConfirmed {
			t.Errorf("expected status Confirmed, got %v", got.Status)
		}
	})

	t.Run("returns error for non-existent key", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		record := &Record{Key: "nonexistent"}
		err := store.Update(record)
		if err != ErrKeyNotFound {
			t.Errorf("expected ErrKeyNotFound, got %v", err)
		}
	})

	t.Run("updates UpdatedAt timestamp", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		record, _ := store.Create("key1")
		originalUpdatedAt := record.UpdatedAt

		time.Sleep(10 * time.Millisecond)

		err := store.Update(record)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !record.UpdatedAt.After(originalUpdatedAt) {
			t.Error("expected UpdatedAt to be updated")
		}
	})
}

func TestInMemoryStore_Delete(t *testing.T) {
	t.Run("deletes existing record", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		_, _ = store.Create("key1")

		err := store.Delete("key1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if store.Size() != 0 {
			t.Errorf("expected store to be empty, got size %d", store.Size())
		}
	})

	t.Run("no error for non-existent key", func(t *testing.T) {
		store := NewInMemoryStore(0)
		defer store.Stop()

		err := store.Delete("nonexistent")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}

func TestInMemoryStore_Cleanup(t *testing.T) {
	store := NewInMemoryStore(50 * time.Millisecond)
	defer store.Stop()

	_, _ = store.Create("key1")
	_, _ = store.Create("key2")

	if store.Size() != 2 {
		t.Fatalf("expected size 2, got %d", store.Size())
	}

	// Wait for cleanup (TTL + cleanup interval)
	time.Sleep(120 * time.Millisecond)

	if store.Size() != 0 {
		t.Errorf("expected empty store after cleanup, got size %d", store.Size())
	}
}

func TestInMemoryStore_Concurrent(t *testing.T) {
	store := NewInMemoryStore(0)
	defer store.Stop()

	var wg sync.WaitGroup
	numGoroutines := 50
	numOperations := 100

	// Concurrent creates
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := "key" + string(rune(id)) + "_" + string(rune(j))
				_, _ = store.Create(key)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := "key" + string(rune(id)) + "_" + string(rune(j))
				_, _ = store.Get(key)
			}
		}(i)
	}

	// Concurrent deletes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := "key" + string(rune(id)) + "_" + string(rune(j))
				_ = store.Delete(key)
			}
		}(i)
	}

	wg.Wait()

	// If we got here without a race detector complaint, the test passes
}

func TestInMemoryStore_Stop(t *testing.T) {
	store := NewInMemoryStore(100 * time.Millisecond)

	// Should be able to call Stop multiple times
	store.Stop()
	store.Stop()

	// Store should still be usable after stop (just cleanup won't run)
	_, err := store.Create("key1")
	if err != nil {
		t.Errorf("expected store to still work after stop, got error: %v", err)
	}
}
