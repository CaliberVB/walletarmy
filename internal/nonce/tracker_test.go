package nonce

import (
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestNewTracker(t *testing.T) {
	tracker := NewTracker()
	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}
}

func TestTracker_GetSetPendingNonce(t *testing.T) {
	tracker := NewTracker()
	wallet := common.HexToAddress("0x1234567890123456789012345678901234567890")
	chainID := uint64(1)
	networkName := "ethereum"

	// Initially should be nil
	nonce := tracker.GetPendingNonce(wallet, chainID)
	if nonce != nil {
		t.Errorf("expected nil nonce for new wallet, got %v", nonce)
	}

	// Set nonce
	tracker.SetPendingNonce(wallet, chainID, networkName, 5)

	// Get should return next nonce (5 + 1 = 6)
	nonce = tracker.GetPendingNonce(wallet, chainID)
	if nonce == nil {
		t.Fatal("expected non-nil nonce after set")
	}
	if nonce.Uint64() != 6 {
		t.Errorf("expected nonce 6, got %d", nonce.Uint64())
	}
}

func TestTracker_SetPendingNonceSkipsLowerNonce(t *testing.T) {
	tracker := NewTracker()
	wallet := common.HexToAddress("0x1234567890123456789012345678901234567890")
	chainID := uint64(1)
	networkName := "ethereum"

	// Set higher nonce
	tracker.SetPendingNonce(wallet, chainID, networkName, 10)

	// Try to set lower nonce - should be skipped
	tracker.SetPendingNonce(wallet, chainID, networkName, 5)

	// Should still be 10
	nonce := tracker.GetPendingNonce(wallet, chainID)
	if nonce.Uint64() != 11 { // GetPendingNonce returns next nonce
		t.Errorf("expected nonce 11 (10+1), got %d", nonce.Uint64())
	}
}

func TestTracker_AcquireNonce_FirstTransaction(t *testing.T) {
	tracker := NewTracker()
	wallet := common.HexToAddress("0x1234567890123456789012345678901234567890")
	chainID := uint64(1)
	networkName := "ethereum"

	t.Run("uses mined nonce when higher", func(t *testing.T) {
		result, err := tracker.AcquireNonce(wallet, chainID, networkName, 10, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Nonce != 10 {
			t.Errorf("expected nonce 10, got %d", result.Nonce)
		}
	})
}

func TestTracker_AcquireNonce_UsesRemotePending(t *testing.T) {
	tracker := NewTracker()
	wallet := common.HexToAddress("0x2234567890123456789012345678901234567890")
	chainID := uint64(1)
	networkName := "ethereum"

	result, err := tracker.AcquireNonce(wallet, chainID, networkName, 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Nonce != 10 {
		t.Errorf("expected nonce 10, got %d", result.Nonce)
	}
}

func TestTracker_AcquireNonce_AbnormalState(t *testing.T) {
	tracker := NewTracker()
	wallet := common.HexToAddress("0x3234567890123456789012345678901234567890")
	chainID := uint64(1)
	networkName := "ethereum"

	// First set a local nonce
	tracker.SetPendingNonce(wallet, chainID, networkName, 5)

	// Now try to acquire with mined > remote pending (abnormal)
	_, err := tracker.AcquireNonce(wallet, chainID, networkName, 10, 5)
	if err != ErrAbnormalNonceState {
		t.Errorf("expected ErrAbnormalNonceState, got %v", err)
	}
}

func TestTracker_ReleaseNonce(t *testing.T) {
	tracker := NewTracker()
	wallet := common.HexToAddress("0x4234567890123456789012345678901234567890")
	chainID := uint64(1)
	networkName := "ethereum"

	t.Run("releases tip nonce", func(t *testing.T) {
		tracker.SetPendingNonce(wallet, chainID, networkName, 5)

		tracker.ReleaseNonce(wallet, chainID, networkName, 5)

		// GetPendingNonce should now return 5 (4+1)
		nonce := tracker.GetPendingNonce(wallet, chainID)
		if nonce.Uint64() != 5 {
			t.Errorf("expected nonce 5 after release, got %d", nonce.Uint64())
		}
	})

	t.Run("skips non-tip nonce", func(t *testing.T) {
		tracker.SetPendingNonce(wallet, chainID, networkName, 10)

		// Try to release non-tip nonce
		tracker.ReleaseNonce(wallet, chainID, networkName, 5)

		// Should still be at 10
		nonce := tracker.GetPendingNonce(wallet, chainID)
		if nonce.Uint64() != 11 {
			t.Errorf("expected nonce 11, got %d", nonce.Uint64())
		}
	})

	t.Run("handles nonce zero", func(t *testing.T) {
		wallet2 := common.HexToAddress("0x5234567890123456789012345678901234567890")
		tracker.SetPendingNonce(wallet2, chainID, networkName, 0)
		tracker.ReleaseNonce(wallet2, chainID, networkName, 0)

		nonce := tracker.GetPendingNonce(wallet2, chainID)
		if nonce != nil {
			t.Errorf("expected nil nonce after releasing 0, got %v", nonce)
		}
	})
}

func TestTracker_MultipleWallets(t *testing.T) {
	tracker := NewTracker()
	wallet1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	wallet2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	chainID := uint64(1)
	networkName := "ethereum"

	tracker.SetPendingNonce(wallet1, chainID, networkName, 10)
	tracker.SetPendingNonce(wallet2, chainID, networkName, 20)

	nonce1 := tracker.GetPendingNonce(wallet1, chainID)
	nonce2 := tracker.GetPendingNonce(wallet2, chainID)

	if nonce1.Uint64() != 11 {
		t.Errorf("wallet1: expected nonce 11, got %d", nonce1.Uint64())
	}
	if nonce2.Uint64() != 21 {
		t.Errorf("wallet2: expected nonce 21, got %d", nonce2.Uint64())
	}
}

func TestTracker_MultipleNetworks(t *testing.T) {
	tracker := NewTracker()
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111111")
	chainID1 := uint64(1)
	chainID2 := uint64(137)

	tracker.SetPendingNonce(wallet, chainID1, "ethereum", 10)
	tracker.SetPendingNonce(wallet, chainID2, "polygon", 20)

	nonce1 := tracker.GetPendingNonce(wallet, chainID1)
	nonce2 := tracker.GetPendingNonce(wallet, chainID2)

	if nonce1.Uint64() != 11 {
		t.Errorf("ethereum: expected nonce 11, got %d", nonce1.Uint64())
	}
	if nonce2.Uint64() != 21 {
		t.Errorf("polygon: expected nonce 21, got %d", nonce2.Uint64())
	}
}

func TestTracker_Concurrent(t *testing.T) {
	tracker := NewTracker()
	wallet := common.HexToAddress("0x1234567890123456789012345678901234567890")
	chainID := uint64(1)
	networkName := "ethereum"

	var wg sync.WaitGroup
	numGoroutines := 50
	numOperations := 100

	// Concurrent sets
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				tracker.SetPendingNonce(wallet, chainID, networkName, uint64(id*numOperations+j))
			}
		}(i)
	}

	// Concurrent gets
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_ = tracker.GetPendingNonce(wallet, chainID)
			}
		}()
	}

	// Concurrent acquires
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_, _ = tracker.AcquireNonce(wallet, chainID, networkName, 100, 100)
			}
		}()
	}

	wg.Wait()

	// If we got here without a race detector complaint, the test passes
}
