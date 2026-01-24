package walletarmy

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/gethclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/util/account"
)

// mockNonceStore is a thread-safe in-memory implementation of NonceStore for testing
type mockNonceStore struct {
	mu     sync.RWMutex
	states map[string]*NonceState // key: wallet+chainID

	// Call tracking
	GetCalls []struct {
		Wallet  common.Address
		ChainID uint64
	}
	SaveCalls             []NonceState
	SavePendingNonceCalls []struct {
		Wallet         common.Address
		ChainID, Nonce uint64
	}
	AddReservedNonceCalls []struct {
		Wallet         common.Address
		ChainID, Nonce uint64
	}
	RemoveReservedNonceCalls []struct {
		Wallet         common.Address
		ChainID, Nonce uint64
	}
	ListAllCalls int

	// Error injection
	GetErr                 error
	SaveErr                error
	SavePendingNonceErr    error
	AddReservedNonceErr    error
	RemoveReservedNonceErr error
	ListAllErr             error
}

func newMockNonceStore() *mockNonceStore {
	return &mockNonceStore{
		states: make(map[string]*NonceState),
	}
}

func (m *mockNonceStore) makeKey(wallet common.Address, chainID uint64) string {
	return wallet.Hex() + "-" + string(rune(chainID))
}

func (m *mockNonceStore) Get(ctx context.Context, wallet common.Address, chainID uint64) (*NonceState, error) {
	m.mu.Lock()
	m.GetCalls = append(m.GetCalls, struct {
		Wallet  common.Address
		ChainID uint64
	}{wallet, chainID})
	m.mu.Unlock()

	if m.GetErr != nil {
		return nil, m.GetErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.states[m.makeKey(wallet, chainID)]
	return state, nil
}

func (m *mockNonceStore) Save(ctx context.Context, state *NonceState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SaveCalls = append(m.SaveCalls, *state)

	if m.SaveErr != nil {
		return m.SaveErr
	}

	// Deep copy
	stateCopy := *state
	if state.LocalPendingNonce != nil {
		n := *state.LocalPendingNonce
		stateCopy.LocalPendingNonce = &n
	}
	stateCopy.ReservedNonces = make([]uint64, len(state.ReservedNonces))
	copy(stateCopy.ReservedNonces, state.ReservedNonces)

	m.states[m.makeKey(state.Wallet, state.ChainID)] = &stateCopy
	return nil
}

func (m *mockNonceStore) SavePendingNonce(ctx context.Context, wallet common.Address, chainID uint64, nonce uint64) error {
	m.mu.Lock()
	m.SavePendingNonceCalls = append(m.SavePendingNonceCalls, struct {
		Wallet         common.Address
		ChainID, Nonce uint64
	}{wallet, chainID, nonce})
	m.mu.Unlock()

	if m.SavePendingNonceErr != nil {
		return m.SavePendingNonceErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.makeKey(wallet, chainID)
	state := m.states[key]
	if state == nil {
		state = &NonceState{Wallet: wallet, ChainID: chainID}
		m.states[key] = state
	}
	state.LocalPendingNonce = &nonce
	state.UpdatedAt = time.Now()
	return nil
}

func (m *mockNonceStore) AddReservedNonce(ctx context.Context, wallet common.Address, chainID uint64, nonce uint64) error {
	m.mu.Lock()
	m.AddReservedNonceCalls = append(m.AddReservedNonceCalls, struct {
		Wallet         common.Address
		ChainID, Nonce uint64
	}{wallet, chainID, nonce})
	m.mu.Unlock()

	if m.AddReservedNonceErr != nil {
		return m.AddReservedNonceErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.makeKey(wallet, chainID)
	state := m.states[key]
	if state == nil {
		state = &NonceState{Wallet: wallet, ChainID: chainID}
		m.states[key] = state
	}
	state.ReservedNonces = append(state.ReservedNonces, nonce)
	state.UpdatedAt = time.Now()
	return nil
}

func (m *mockNonceStore) RemoveReservedNonce(ctx context.Context, wallet common.Address, chainID uint64, nonce uint64) error {
	m.mu.Lock()
	m.RemoveReservedNonceCalls = append(m.RemoveReservedNonceCalls, struct {
		Wallet         common.Address
		ChainID, Nonce uint64
	}{wallet, chainID, nonce})
	m.mu.Unlock()

	if m.RemoveReservedNonceErr != nil {
		return m.RemoveReservedNonceErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.makeKey(wallet, chainID)
	state := m.states[key]
	if state == nil {
		return nil
	}
	// Remove nonce from reserved list
	newReserved := make([]uint64, 0, len(state.ReservedNonces))
	for _, n := range state.ReservedNonces {
		if n != nonce {
			newReserved = append(newReserved, n)
		}
	}
	state.ReservedNonces = newReserved
	state.UpdatedAt = time.Now()
	return nil
}

func (m *mockNonceStore) ListAll(ctx context.Context) ([]*NonceState, error) {
	m.mu.Lock()
	m.ListAllCalls++
	m.mu.Unlock()

	if m.ListAllErr != nil {
		return nil, m.ListAllErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*NonceState, 0, len(m.states))
	for _, state := range m.states {
		result = append(result, state)
	}
	return result, nil
}

// mockTxStore is a thread-safe in-memory implementation of TxStore for testing
type mockTxStore struct {
	mu  sync.RWMutex
	txs map[common.Hash]*PendingTx

	// Call tracking
	SaveCalls       []PendingTx
	GetCalls        []common.Hash
	GetByNonceCalls []struct {
		Wallet         common.Address
		ChainID, Nonce uint64
	}
	ListPendingCalls []struct {
		Wallet  common.Address
		ChainID uint64
	}
	ListAllPendingCalls int
	UpdateStatusCalls   []struct {
		Hash    common.Hash
		Status  PendingTxStatus
		Receipt *types.Receipt
	}
	DeleteCalls          []common.Hash
	DeleteOlderThanCalls []time.Duration

	// Error injection
	SaveErr            error
	GetErr             error
	GetByNonceErr      error
	ListPendingErr     error
	ListAllPendingErr  error
	UpdateStatusErr    error
	DeleteErr          error
	DeleteOlderThanErr error
}

func newMockTxStore() *mockTxStore {
	return &mockTxStore{
		txs: make(map[common.Hash]*PendingTx),
	}
}

func (m *mockTxStore) Save(ctx context.Context, tx *PendingTx) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SaveCalls = append(m.SaveCalls, *tx)

	if m.SaveErr != nil {
		return m.SaveErr
	}

	// Deep copy
	txCopy := *tx
	m.txs[tx.Hash] = &txCopy
	return nil
}

func (m *mockTxStore) Get(ctx context.Context, hash common.Hash) (*PendingTx, error) {
	m.mu.Lock()
	m.GetCalls = append(m.GetCalls, hash)
	m.mu.Unlock()

	if m.GetErr != nil {
		return nil, m.GetErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.txs[hash], nil
}

func (m *mockTxStore) GetByNonce(ctx context.Context, wallet common.Address, chainID uint64, nonce uint64) ([]*PendingTx, error) {
	m.mu.Lock()
	m.GetByNonceCalls = append(m.GetByNonceCalls, struct {
		Wallet         common.Address
		ChainID, Nonce uint64
	}{wallet, chainID, nonce})
	m.mu.Unlock()

	if m.GetByNonceErr != nil {
		return nil, m.GetByNonceErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*PendingTx
	for _, tx := range m.txs {
		if tx.Wallet == wallet && tx.ChainID == chainID && tx.Nonce == nonce {
			result = append(result, tx)
		}
	}
	return result, nil
}

func (m *mockTxStore) ListPending(ctx context.Context, wallet common.Address, chainID uint64) ([]*PendingTx, error) {
	m.mu.Lock()
	m.ListPendingCalls = append(m.ListPendingCalls, struct {
		Wallet  common.Address
		ChainID uint64
	}{wallet, chainID})
	m.mu.Unlock()

	if m.ListPendingErr != nil {
		return nil, m.ListPendingErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*PendingTx
	for _, tx := range m.txs {
		if tx.Wallet == wallet && tx.ChainID == chainID &&
			(tx.Status == PendingTxStatusPending || tx.Status == PendingTxStatusBroadcasted) {
			result = append(result, tx)
		}
	}
	return result, nil
}

func (m *mockTxStore) ListAllPending(ctx context.Context) ([]*PendingTx, error) {
	m.mu.Lock()
	m.ListAllPendingCalls++
	m.mu.Unlock()

	if m.ListAllPendingErr != nil {
		return nil, m.ListAllPendingErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*PendingTx
	for _, tx := range m.txs {
		if tx.Status == PendingTxStatusPending || tx.Status == PendingTxStatusBroadcasted {
			result = append(result, tx)
		}
	}
	return result, nil
}

func (m *mockTxStore) UpdateStatus(ctx context.Context, hash common.Hash, status PendingTxStatus, receipt *types.Receipt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdateStatusCalls = append(m.UpdateStatusCalls, struct {
		Hash    common.Hash
		Status  PendingTxStatus
		Receipt *types.Receipt
	}{hash, status, receipt})

	if m.UpdateStatusErr != nil {
		return m.UpdateStatusErr
	}

	if tx, ok := m.txs[hash]; ok {
		tx.Status = status
		tx.Receipt = receipt
		tx.UpdatedAt = time.Now()
	}
	return nil
}

func (m *mockTxStore) Delete(ctx context.Context, hash common.Hash) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteCalls = append(m.DeleteCalls, hash)

	if m.DeleteErr != nil {
		return m.DeleteErr
	}

	delete(m.txs, hash)
	return nil
}

func (m *mockTxStore) DeleteOlderThan(ctx context.Context, age time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteOlderThanCalls = append(m.DeleteOlderThanCalls, age)

	if m.DeleteOlderThanErr != nil {
		return 0, m.DeleteOlderThanErr
	}

	cutoff := time.Now().Add(-age)
	count := 0
	for hash, tx := range m.txs {
		if tx.CreatedAt.Before(cutoff) {
			delete(m.txs, hash)
			count++
		}
	}
	return count, nil
}

// --- Tests ---

func TestPendingTxStatus_Constants(t *testing.T) {
	assert.Equal(t, PendingTxStatus("pending"), PendingTxStatusPending)
	assert.Equal(t, PendingTxStatus("broadcasted"), PendingTxStatusBroadcasted)
	assert.Equal(t, PendingTxStatus("mined"), PendingTxStatusMined)
	assert.Equal(t, PendingTxStatus("reverted"), PendingTxStatusReverted)
	assert.Equal(t, PendingTxStatus("dropped"), PendingTxStatusDropped)
	assert.Equal(t, PendingTxStatus("replaced"), PendingTxStatusReplaced)
}

func TestDefaultRecoveryOptions(t *testing.T) {
	opts := DefaultRecoveryOptions()

	assert.True(t, opts.ResumeMonitoring)
	assert.Equal(t, 5*time.Second, opts.TxCheckInterval)
	assert.Equal(t, 10, opts.MaxConcurrentMonitors)
	assert.Nil(t, opts.OnTxRecovered)
	assert.Nil(t, opts.OnTxMined)
	assert.Nil(t, opts.OnTxDropped)
}

func TestWithNonceStore(t *testing.T) {
	store := newMockNonceStore()
	wm := NewWalletManager(WithNonceStore(store))

	assert.Equal(t, store, wm.NonceStore())
}

func TestWithTxStore(t *testing.T) {
	store := newMockTxStore()
	wm := NewWalletManager(WithTxStore(store))

	assert.Equal(t, store, wm.TxStore())
}

func TestWalletManager_NonceStore_Nil(t *testing.T) {
	wm := NewWalletManager()
	assert.Nil(t, wm.NonceStore())
}

func TestWalletManager_TxStore_Nil(t *testing.T) {
	wm := NewWalletManager()
	assert.Nil(t, wm.TxStore())
}

func TestMockNonceStore_BasicOperations(t *testing.T) {
	store := newMockNonceStore()
	ctx := context.Background()
	wallet := common.HexToAddress("0x1234567890123456789012345678901234567890")
	chainID := uint64(1)

	// Get non-existent state
	state, err := store.Get(ctx, wallet, chainID)
	require.NoError(t, err)
	assert.Nil(t, state)

	// Save pending nonce
	err = store.SavePendingNonce(ctx, wallet, chainID, 5)
	require.NoError(t, err)

	// Get saved state
	state, err = store.Get(ctx, wallet, chainID)
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, wallet, state.Wallet)
	assert.Equal(t, chainID, state.ChainID)
	require.NotNil(t, state.LocalPendingNonce)
	assert.Equal(t, uint64(5), *state.LocalPendingNonce)

	// Add reserved nonce
	err = store.AddReservedNonce(ctx, wallet, chainID, 6)
	require.NoError(t, err)

	state, err = store.Get(ctx, wallet, chainID)
	require.NoError(t, err)
	assert.Contains(t, state.ReservedNonces, uint64(6))

	// Remove reserved nonce
	err = store.RemoveReservedNonce(ctx, wallet, chainID, 6)
	require.NoError(t, err)

	state, err = store.Get(ctx, wallet, chainID)
	require.NoError(t, err)
	assert.NotContains(t, state.ReservedNonces, uint64(6))
}

func TestMockNonceStore_ListAll(t *testing.T) {
	store := newMockNonceStore()
	ctx := context.Background()

	wallet1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	wallet2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	_ = store.SavePendingNonce(ctx, wallet1, 1, 10)
	_ = store.SavePendingNonce(ctx, wallet2, 1, 20)
	_ = store.SavePendingNonce(ctx, wallet1, 56, 5)

	states, err := store.ListAll(ctx)
	require.NoError(t, err)
	assert.Len(t, states, 3)
}

func TestMockTxStore_BasicOperations(t *testing.T) {
	store := newMockTxStore()
	ctx := context.Background()
	hash := common.HexToHash("0xabcd")
	wallet := common.HexToAddress("0x1234")

	// Get non-existent tx
	tx, err := store.Get(ctx, hash)
	require.NoError(t, err)
	assert.Nil(t, tx)

	// Save tx
	ptx := &PendingTx{
		Hash:      hash,
		Wallet:    wallet,
		ChainID:   1,
		Nonce:     5,
		Status:    PendingTxStatusBroadcasted,
		CreatedAt: time.Now(),
	}
	err = store.Save(ctx, ptx)
	require.NoError(t, err)

	// Get saved tx
	tx, err = store.Get(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, hash, tx.Hash)
	assert.Equal(t, PendingTxStatusBroadcasted, tx.Status)

	// Update status
	receipt := &types.Receipt{Status: types.ReceiptStatusSuccessful}
	err = store.UpdateStatus(ctx, hash, PendingTxStatusMined, receipt)
	require.NoError(t, err)

	tx, err = store.Get(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, PendingTxStatusMined, tx.Status)
	assert.NotNil(t, tx.Receipt)

	// Delete
	err = store.Delete(ctx, hash)
	require.NoError(t, err)

	tx, err = store.Get(ctx, hash)
	require.NoError(t, err)
	assert.Nil(t, tx)
}

func TestMockTxStore_ListPending(t *testing.T) {
	store := newMockTxStore()
	ctx := context.Background()
	wallet := common.HexToAddress("0x1234")

	// Add various txs
	txs := []*PendingTx{
		{Hash: common.HexToHash("0x01"), Wallet: wallet, ChainID: 1, Status: PendingTxStatusPending},
		{Hash: common.HexToHash("0x02"), Wallet: wallet, ChainID: 1, Status: PendingTxStatusBroadcasted},
		{Hash: common.HexToHash("0x03"), Wallet: wallet, ChainID: 1, Status: PendingTxStatusMined},
		{Hash: common.HexToHash("0x04"), Wallet: wallet, ChainID: 56, Status: PendingTxStatusPending},
	}

	for _, tx := range txs {
		_ = store.Save(ctx, tx)
	}

	// List pending for wallet on chain 1
	pending, err := store.ListPending(ctx, wallet, 1)
	require.NoError(t, err)
	assert.Len(t, pending, 2) // Only pending and broadcasted

	// List all pending
	allPending, err := store.ListAllPending(ctx)
	require.NoError(t, err)
	assert.Len(t, allPending, 3) // All pending/broadcasted across chains
}

func TestMockTxStore_GetByNonce(t *testing.T) {
	store := newMockTxStore()
	ctx := context.Background()
	wallet := common.HexToAddress("0x1234")

	// Add txs with same nonce (replacement)
	txs := []*PendingTx{
		{Hash: common.HexToHash("0x01"), Wallet: wallet, ChainID: 1, Nonce: 5, Status: PendingTxStatusReplaced},
		{Hash: common.HexToHash("0x02"), Wallet: wallet, ChainID: 1, Nonce: 5, Status: PendingTxStatusPending},
		{Hash: common.HexToHash("0x03"), Wallet: wallet, ChainID: 1, Nonce: 6, Status: PendingTxStatusPending},
	}

	for _, tx := range txs {
		_ = store.Save(ctx, tx)
	}

	// Get by nonce 5
	result, err := store.GetByNonce(ctx, wallet, 1, 5)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestMockTxStore_DeleteOlderThan(t *testing.T) {
	store := newMockTxStore()
	ctx := context.Background()

	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now()

	txs := []*PendingTx{
		{Hash: common.HexToHash("0x01"), CreatedAt: oldTime},
		{Hash: common.HexToHash("0x02"), CreatedAt: oldTime},
		{Hash: common.HexToHash("0x03"), CreatedAt: newTime},
	}

	for _, tx := range txs {
		_ = store.Save(ctx, tx)
	}

	count, err := store.DeleteOlderThan(ctx, 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Verify only new tx remains
	tx, _ := store.Get(ctx, common.HexToHash("0x03"))
	assert.NotNil(t, tx)
	tx, _ = store.Get(ctx, common.HexToHash("0x01"))
	assert.Nil(t, tx)
}

func TestPersistNonceAcquisition_NoStore(t *testing.T) {
	wm := NewWalletManager()
	// Should not panic when no store is configured
	wm.persistNonceAcquisition(testAddr1, 1, 5)
}

func TestPersistNonceAcquisition_WithStore(t *testing.T) {
	store := newMockNonceStore()
	wm := NewWalletManager(WithNonceStore(store))

	wm.persistNonceAcquisition(testAddr1, 1, 5)

	assert.Len(t, store.AddReservedNonceCalls, 1)
	assert.Equal(t, testAddr1, store.AddReservedNonceCalls[0].Wallet)
	assert.Equal(t, uint64(1), store.AddReservedNonceCalls[0].ChainID)
	assert.Equal(t, uint64(5), store.AddReservedNonceCalls[0].Nonce)

	assert.Len(t, store.SavePendingNonceCalls, 1)
	assert.Equal(t, uint64(5), store.SavePendingNonceCalls[0].Nonce)
}

func TestPersistNonceRelease_NoStore(t *testing.T) {
	wm := NewWalletManager()
	// Should not panic when no store is configured
	wm.persistNonceRelease(testAddr1, 1, 5)
}

func TestPersistNonceRelease_WithStore(t *testing.T) {
	store := newMockNonceStore()
	wm := NewWalletManager(WithNonceStore(store))

	wm.persistNonceRelease(testAddr1, 1, 5)

	assert.Len(t, store.RemoveReservedNonceCalls, 1)
	assert.Equal(t, testAddr1, store.RemoveReservedNonceCalls[0].Wallet)
	assert.Equal(t, uint64(1), store.RemoveReservedNonceCalls[0].ChainID)
	assert.Equal(t, uint64(5), store.RemoveReservedNonceCalls[0].Nonce)
}

func TestPersistTxBroadcast_NoStore(t *testing.T) {
	wm := NewWalletManager()
	tx := types.NewTx(&types.LegacyTx{Nonce: 5})
	// Should not panic when no store is configured
	wm.persistTxBroadcast(tx, testAddr1, 1)
}

func TestPersistTxBroadcast_WithStore(t *testing.T) {
	store := newMockTxStore()
	wm := NewWalletManager(WithTxStore(store))

	tx := types.NewTx(&types.LegacyTx{Nonce: 5})
	wm.persistTxBroadcast(tx, testAddr1, 1)

	assert.Len(t, store.SaveCalls, 1)
	assert.Equal(t, tx.Hash(), store.SaveCalls[0].Hash)
	assert.Equal(t, testAddr1, store.SaveCalls[0].Wallet)
	assert.Equal(t, uint64(1), store.SaveCalls[0].ChainID)
	assert.Equal(t, uint64(5), store.SaveCalls[0].Nonce)
	assert.Equal(t, PendingTxStatusBroadcasted, store.SaveCalls[0].Status)
}

func TestPersistTxMined_NoStore(t *testing.T) {
	wm := NewWalletManager()
	// Should not panic when no store is configured
	wm.persistTxMined(common.HexToHash("0x1234"), PendingTxStatusMined, nil)
}

func TestPersistTxMined_WithStore(t *testing.T) {
	store := newMockTxStore()
	wm := NewWalletManager(WithTxStore(store))

	hash := common.HexToHash("0x1234")
	receipt := &types.Receipt{Status: types.ReceiptStatusSuccessful}
	wm.persistTxMined(hash, PendingTxStatusMined, receipt)

	assert.Len(t, store.UpdateStatusCalls, 1)
	assert.Equal(t, hash, store.UpdateStatusCalls[0].Hash)
	assert.Equal(t, PendingTxStatusMined, store.UpdateStatusCalls[0].Status)
	assert.Equal(t, receipt, store.UpdateStatusCalls[0].Receipt)
}

func TestRecover_NoStores(t *testing.T) {
	wm := NewWalletManager()
	ctx := context.Background()

	result, err := wm.Recover(ctx)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 0, result.RecoveredTxs)
	assert.Equal(t, 0, result.MinedTxs)
	assert.Equal(t, 0, result.DroppedTxs)
	assert.Equal(t, 0, result.ReconciledNonces)
}

func TestRecoverWithOptions_NoStores(t *testing.T) {
	wm := NewWalletManager()
	ctx := context.Background()

	opts := DefaultRecoveryOptions()
	opts.ResumeMonitoring = false

	result, err := wm.RecoverWithOptions(ctx, opts)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestNonceState_Fields(t *testing.T) {
	nonce := uint64(42)
	state := NonceState{
		Wallet:            testAddr1,
		ChainID:           1,
		LocalPendingNonce: &nonce,
		ReservedNonces:    []uint64{43, 44},
		UpdatedAt:         time.Now(),
	}

	assert.Equal(t, testAddr1, state.Wallet)
	assert.Equal(t, uint64(1), state.ChainID)
	assert.NotNil(t, state.LocalPendingNonce)
	assert.Equal(t, uint64(42), *state.LocalPendingNonce)
	assert.Len(t, state.ReservedNonces, 2)
}

func TestPendingTx_Fields(t *testing.T) {
	tx := types.NewTx(&types.LegacyTx{Nonce: 5})
	receipt := &types.Receipt{Status: types.ReceiptStatusSuccessful}

	ptx := PendingTx{
		Hash:        tx.Hash(),
		Wallet:      testAddr1,
		ChainID:     1,
		Nonce:       5,
		Status:      PendingTxStatusMined,
		Transaction: tx,
		Receipt:     receipt,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Metadata:    map[string]string{"key": "value"},
	}

	assert.Equal(t, tx.Hash(), ptx.Hash)
	assert.Equal(t, testAddr1, ptx.Wallet)
	assert.Equal(t, uint64(1), ptx.ChainID)
	assert.Equal(t, uint64(5), ptx.Nonce)
	assert.Equal(t, PendingTxStatusMined, ptx.Status)
	assert.Equal(t, tx, ptx.Transaction)
	assert.Equal(t, receipt, ptx.Receipt)
	assert.Equal(t, "value", ptx.Metadata["key"])
}

func TestRecoveryResult_Fields(t *testing.T) {
	result := RecoveryResult{
		RecoveredTxs:     3,
		MinedTxs:         5,
		DroppedTxs:       2,
		ReconciledNonces: 4,
		Errors:           []error{assert.AnError},
	}

	assert.Equal(t, 3, result.RecoveredTxs)
	assert.Equal(t, 5, result.MinedTxs)
	assert.Equal(t, 2, result.DroppedTxs)
	assert.Equal(t, 4, result.ReconciledNonces)
	assert.Len(t, result.Errors, 1)
}

func TestRecoveryOptions_Callbacks(t *testing.T) {
	recoveredCalled := false
	minedCalled := false
	droppedCalled := false

	opts := RecoveryOptions{
		ResumeMonitoring:      false,
		TxCheckInterval:       time.Second,
		MaxConcurrentMonitors: 5,
		OnTxRecovered: func(tx *PendingTx) {
			recoveredCalled = true
		},
		OnTxMined: func(tx *PendingTx, receipt *types.Receipt) {
			minedCalled = true
		},
		OnTxDropped: func(tx *PendingTx) {
			droppedCalled = true
		},
	}

	// Test callbacks are callable
	opts.OnTxRecovered(&PendingTx{})
	opts.OnTxMined(&PendingTx{}, nil)
	opts.OnTxDropped(&PendingTx{})

	assert.True(t, recoveredCalled)
	assert.True(t, minedCalled)
	assert.True(t, droppedCalled)
}

func TestMockNonceStore_ErrorInjection(t *testing.T) {
	store := newMockNonceStore()
	ctx := context.Background()
	wallet := testAddr1

	// Test Get error
	store.GetErr = assert.AnError
	_, err := store.Get(ctx, wallet, 1)
	assert.Error(t, err)
	store.GetErr = nil

	// Test Save error
	store.SaveErr = assert.AnError
	err = store.Save(ctx, &NonceState{Wallet: wallet, ChainID: 1})
	assert.Error(t, err)
	store.SaveErr = nil

	// Test SavePendingNonce error
	store.SavePendingNonceErr = assert.AnError
	err = store.SavePendingNonce(ctx, wallet, 1, 5)
	assert.Error(t, err)
	store.SavePendingNonceErr = nil

	// Test AddReservedNonce error
	store.AddReservedNonceErr = assert.AnError
	err = store.AddReservedNonce(ctx, wallet, 1, 5)
	assert.Error(t, err)
	store.AddReservedNonceErr = nil

	// Test RemoveReservedNonce error
	store.RemoveReservedNonceErr = assert.AnError
	err = store.RemoveReservedNonce(ctx, wallet, 1, 5)
	assert.Error(t, err)
	store.RemoveReservedNonceErr = nil

	// Test ListAll error
	store.ListAllErr = assert.AnError
	_, err = store.ListAll(ctx)
	assert.Error(t, err)
}

func TestMockTxStore_ErrorInjection(t *testing.T) {
	store := newMockTxStore()
	ctx := context.Background()
	hash := common.HexToHash("0x1234")
	wallet := testAddr1

	// Test Save error
	store.SaveErr = assert.AnError
	err := store.Save(ctx, &PendingTx{Hash: hash})
	assert.Error(t, err)
	store.SaveErr = nil

	// Test Get error
	store.GetErr = assert.AnError
	_, err = store.Get(ctx, hash)
	assert.Error(t, err)
	store.GetErr = nil

	// Test GetByNonce error
	store.GetByNonceErr = assert.AnError
	_, err = store.GetByNonce(ctx, wallet, 1, 5)
	assert.Error(t, err)
	store.GetByNonceErr = nil

	// Test ListPending error
	store.ListPendingErr = assert.AnError
	_, err = store.ListPending(ctx, wallet, 1)
	assert.Error(t, err)
	store.ListPendingErr = nil

	// Test ListAllPending error
	store.ListAllPendingErr = assert.AnError
	_, err = store.ListAllPending(ctx)
	assert.Error(t, err)
	store.ListAllPendingErr = nil

	// Test UpdateStatus error
	store.UpdateStatusErr = assert.AnError
	err = store.UpdateStatus(ctx, hash, PendingTxStatusMined, nil)
	assert.Error(t, err)
	store.UpdateStatusErr = nil

	// Test Delete error
	store.DeleteErr = assert.AnError
	err = store.Delete(ctx, hash)
	assert.Error(t, err)
	store.DeleteErr = nil

	// Test DeleteOlderThan error
	store.DeleteOlderThanErr = assert.AnError
	_, err = store.DeleteOlderThan(ctx, time.Hour)
	assert.Error(t, err)
}

func TestMockNonceStore_RemoveReservedNonce_NonExistent(t *testing.T) {
	store := newMockNonceStore()
	ctx := context.Background()

	// Remove from non-existent wallet - should not error
	err := store.RemoveReservedNonce(ctx, testAddr1, 1, 5)
	require.NoError(t, err)
}

func TestMockTxStore_UpdateStatus_NonExistent(t *testing.T) {
	store := newMockTxStore()
	ctx := context.Background()

	// Update non-existent tx - should not error (no-op)
	err := store.UpdateStatus(ctx, common.HexToHash("0x1234"), PendingTxStatusMined, nil)
	require.NoError(t, err)
}

func TestNewRecoveryHandler(t *testing.T) {
	nonceStore := newMockNonceStore()
	txStore := newMockTxStore()
	wm := NewWalletManager(
		WithNonceStore(nonceStore),
		WithTxStore(txStore),
	)

	handler := newRecoveryHandler(wm)
	assert.NotNil(t, handler)
	assert.Equal(t, wm, handler.wm)
	assert.Equal(t, txStore, handler.txStore)
	assert.Equal(t, nonceStore, handler.nonceStore)
}

func TestMockNonceStore_Save(t *testing.T) {
	store := newMockNonceStore()
	ctx := context.Background()

	nonce := uint64(42)
	state := &NonceState{
		Wallet:            testAddr1,
		ChainID:           1,
		LocalPendingNonce: &nonce,
		ReservedNonces:    []uint64{43, 44},
		UpdatedAt:         time.Now(),
	}

	err := store.Save(ctx, state)
	require.NoError(t, err)

	// Verify saved
	saved, err := store.Get(ctx, testAddr1, 1)
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, uint64(42), *saved.LocalPendingNonce)
	assert.Len(t, saved.ReservedNonces, 2)
}

func TestMockNonceStore_ConcurrentAccess(t *testing.T) {
	store := newMockNonceStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = store.SavePendingNonce(ctx, testAddr1, 1, uint64(n))
			_, _ = store.Get(ctx, testAddr1, 1)
			_ = store.AddReservedNonce(ctx, testAddr1, 1, uint64(n))
			_ = store.RemoveReservedNonce(ctx, testAddr1, 1, uint64(n))
		}(i)
	}
	wg.Wait()

	// Just verify no panics occurred
	_, err := store.ListAll(ctx)
	require.NoError(t, err)
}

func TestMockTxStore_ConcurrentAccess(t *testing.T) {
	store := newMockTxStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			hash := common.BigToHash(common.Big1)
			_ = store.Save(ctx, &PendingTx{Hash: hash, Status: PendingTxStatusPending})
			_, _ = store.Get(ctx, hash)
			_, _ = store.ListAllPending(ctx)
			_ = store.UpdateStatus(ctx, hash, PendingTxStatusMined, nil)
		}(i)
	}
	wg.Wait()

	// Just verify no panics occurred
	_, err := store.ListAllPending(ctx)
	require.NoError(t, err)
}

func TestResumePendingTransaction_NilPendingTx(t *testing.T) {
	wm := NewWalletManager()
	ctx := context.Background()

	_, _, err := wm.ResumePendingTransaction(ctx, nil, ResumeTransactionOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pendingTx cannot be nil")
}

func TestResumePendingTransaction_NilTransaction(t *testing.T) {
	wm := NewWalletManager()
	ctx := context.Background()

	ptx := &PendingTx{
		Hash:        common.HexToHash("0x1234"),
		Wallet:      testAddr1,
		ChainID:     1,
		Transaction: nil, // No transaction data
	}

	_, _, err := wm.ResumePendingTransaction(ctx, ptx, ResumeTransactionOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Transaction cannot be nil")
}

func TestResumePendingTransaction_InvalidChainID(t *testing.T) {
	wm := NewWalletManager()
	ctx := context.Background()

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    5,
		GasPrice: big.NewInt(20e9),
		Gas:      21000,
		To:       &testAddr2,
		Value:    big.NewInt(1e18),
	})

	ptx := &PendingTx{
		Hash:        tx.Hash(),
		Wallet:      testAddr1,
		ChainID:     999999, // Invalid chain ID
		Transaction: tx,
	}

	_, _, err := wm.ResumePendingTransaction(ctx, ptx, ResumeTransactionOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get network")
}

func TestDefaultResumeTransactionOptions(t *testing.T) {
	opts := DefaultResumeTransactionOptions()

	assert.Equal(t, DefaultNumRetries, opts.NumRetries)
	assert.Equal(t, DefaultSleepDuration, opts.SleepDuration)
	assert.Equal(t, DefaultTxCheckInterval, opts.TxCheckInterval)
	assert.Equal(t, float64(0), opts.MaxGasPrice) // 0 means use default multiplier
	assert.Equal(t, float64(0), opts.MaxTipCap)
	assert.Nil(t, opts.TxMinedHook)
}

func TestResumeTransactionOptions_Fields(t *testing.T) {
	hookCalled := false
	opts := ResumeTransactionOptions{
		NumRetries:      5,
		SleepDuration:   time.Second,
		TxCheckInterval: 2 * time.Second,
		MaxGasPrice:     100.0,
		MaxTipCap:       10.0,
		TxMinedHook: func(tx *types.Transaction, receipt *types.Receipt) error {
			hookCalled = true
			return nil
		},
	}

	assert.Equal(t, 5, opts.NumRetries)
	assert.Equal(t, time.Second, opts.SleepDuration)
	assert.Equal(t, 2*time.Second, opts.TxCheckInterval)
	assert.Equal(t, 100.0, opts.MaxGasPrice)
	assert.Equal(t, 10.0, opts.MaxTipCap)

	// Test hook is callable
	_ = opts.TxMinedHook(nil, nil)
	assert.True(t, hookCalled)
}

func TestTxExecutionContext_InitialTx_Field(t *testing.T) {
	tx := types.NewTx(&types.LegacyTx{Nonce: 5})

	execCtx := &TxExecutionContext{
		InitialTx: tx,
	}

	assert.Equal(t, tx, execCtx.InitialTx)

	// Simulate clearing after use
	execCtx.InitialTx = nil
	assert.Nil(t, execCtx.InitialTx)
}

// ============================================================
// Integration Tests: Recovery Flow
// ============================================================

// TestRecoveryFlow_BasicRecovery_TxAlreadyMined tests the basic recovery flow
// where a pending transaction was already mined while the app was down.
// This is the simplest recovery scenario.
func TestRecoveryFlow_BasicRecovery_TxAlreadyMined(t *testing.T) {
	nonceStore := newMockNonceStore()
	txStore := newMockTxStore()

	// Create a signed transaction
	originalNonce := uint64(5)
	originalTx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     originalNonce,
		GasTipCap: big.NewInt(2e9),
		GasFeeCap: big.NewInt(22e9),
		Gas:       21000,
		To:        &testAddr2,
		Value:     oneEth,
	})
	signedTx, err := types.SignTx(originalTx, types.NewLondonSigner(big.NewInt(1)), testPrivateKey1)
	require.NoError(t, err)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	// Save the pending transaction to store as if it was broadcast before crash
	pendingTx := &PendingTx{
		Hash:        signedTx.Hash(),
		Wallet:      fromAddr,
		ChainID:     1,
		Nonce:       originalNonce,
		Status:      PendingTxStatusBroadcasted,
		Transaction: signedTx,
		CreatedAt:   time.Now().Add(-1 * time.Minute),
		UpdatedAt:   time.Now().Add(-1 * time.Minute),
	}
	err = txStore.Save(context.Background(), pendingTx)
	require.NoError(t, err)

	// Track hook calls
	var recoveredTxs []*PendingTx
	var minedTxs []*PendingTx
	hookMu := sync.Mutex{}

	// Create mock reader that returns "done" (already mined)
	expectedReceipt := &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		TxHash:      signedTx.Hash(),
		BlockNumber: big.NewInt(12345678),
		GasUsed:     21000,
	}
	mockReader := &mockEthReader{
		GetMinedNonceFn:   func(addr string) (uint64, error) { return originalNonce + 1, nil },
		GetPendingNonceFn: func(addr string) (uint64, error) { return originalNonce + 1, nil },
		TxInfoFromHashFn: func(hash string) (TxInfo, error) {
			return TxInfo{
				Status:  "done",
				Receipt: expectedReceipt,
			}, nil
		},
	}

	wm := NewWalletManager(
		WithNonceStore(nonceStore),
		WithTxStore(txStore),
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return mockReader, nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return &mockEthBroadcaster{}, nil
		}),
	)

	// Initialize network
	_, err = wm.Reader(networks.EthereumMainnet)
	require.NoError(t, err)

	ctx := context.Background()
	opts := RecoveryOptions{
		ResumeMonitoring:      true,
		TxCheckInterval:       10 * time.Millisecond,
		MaxConcurrentMonitors: 5,
		OnTxRecovered: func(tx *PendingTx) {
			hookMu.Lock()
			recoveredTxs = append(recoveredTxs, tx)
			hookMu.Unlock()
		},
		OnTxMined: func(tx *PendingTx, receipt *types.Receipt) {
			hookMu.Lock()
			minedTxs = append(minedTxs, tx)
			hookMu.Unlock()
		},
	}

	result, err := wm.RecoverWithOptions(ctx, opts)
	require.NoError(t, err)

	// For already mined tx, it should be counted in MinedTxs (not RecoveredTxs)
	assert.Equal(t, 0, result.RecoveredTxs, "tx was already mined, not pending")
	assert.Equal(t, 1, result.MinedTxs, "should have found 1 mined tx")
	assert.Equal(t, 0, result.DroppedTxs, "no txs should be dropped")

	// OnTxMined should be called (not OnTxRecovered for already mined tx)
	hookMu.Lock()
	assert.Len(t, recoveredTxs, 0, "OnTxRecovered should not be called for already mined tx")
	assert.Len(t, minedTxs, 1, "OnTxMined should be called")
	if len(minedTxs) > 0 {
		assert.Equal(t, signedTx.Hash(), minedTxs[0].Hash)
	}
	hookMu.Unlock()

	// Verify tx status was updated in store
	txStore.mu.RLock()
	storedTx := txStore.txs[signedTx.Hash()]
	txStore.mu.RUnlock()

	require.NotNil(t, storedTx)
	assert.Equal(t, PendingTxStatusMined, storedTx.Status)
}

// TestRecoveryFlow_FullIntegration_SlowTx_GasBump_Replacement tests the complete
// crash recovery flow with gas bumping:
// 1. Execute tx with persistence → broadcasted → persisted to TxStore
// 2. Simulate crash/restart → create new WalletManager with same stores
// 3. Call Recover() → finds pending tx in store
// 4. Pending tx is slow → monitor returns "slow" status
// 5. Gas bump + replacement tx → new tx broadcasted with higher gas
// 6. Original tx marked as replaced → PendingTxStatusReplaced
// 7. New tx mined → status updated
// 8. Hooks called correctly → OnTxRecovered, OnTxMined invoked
//
// TestRecoveryFlow_FullIntegration_SlowTx_GasBump_Replacement tests the complete
// crash recovery flow with gas bumping. Uses a mock network that doesn't support
// sync transactions, so the slow tx -> gas bump flow can be tested.
func TestRecoveryFlow_FullIntegration_SlowTx_GasBump_Replacement(t *testing.T) {
	// ========================================
	// Setup: Mock network without sync tx support
	// ========================================
	// Use a mock network that doesn't support sync tx - this enables the slow tx flow
	// For networks with sync tx support, txs are either mined immediately or fail,
	// so there's no "pending" state to monitor and no gas bumping.
	testChainID := uint64(9999) // Use a test chain ID
	mockNetwork := newMockNetworkNoSyncTx(testChainID, "test-network")

	// ========================================
	// Setup: Shared persistence stores
	// ========================================
	nonceStore := newMockNonceStore()
	txStore := newMockTxStore()

	// Create a signed transaction that we'll use as the "pending" tx
	// We need to sign it so it can be properly resumed
	originalNonce := uint64(5)
	originalTipCap := big.NewInt(2e9)     // 2 gwei
	originalGasFeeCap := big.NewInt(22e9) // 22 gwei (baseFee + tip)

	// Create the original pending transaction (EIP-1559)
	originalTx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(int64(testChainID)), // Use our test chain ID
		Nonce:     originalNonce,
		GasTipCap: originalTipCap,
		GasFeeCap: originalGasFeeCap,
		Gas:       21000,
		To:        &testAddr2,
		Value:     oneEth,
		Data:      nil,
	})

	// Derive the address from the private key
	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	// Sign the transaction so it can be used in recovery
	signedTx, err := types.SignTx(originalTx, types.NewLondonSigner(big.NewInt(int64(testChainID))), testPrivateKey1)
	require.NoError(t, err)

	// ========================================
	// Step 1: Simulate initial broadcast (pre-crash state)
	// ========================================
	// Save the pending transaction to the store as if it was broadcast before crash
	pendingTx := &PendingTx{
		Hash:        signedTx.Hash(),
		Wallet:      fromAddr,
		ChainID:     testChainID,
		Nonce:       originalNonce,
		Status:      PendingTxStatusBroadcasted,
		Transaction: signedTx,
		CreatedAt:   time.Now().Add(-1 * time.Minute), // Created 1 minute ago
		UpdatedAt:   time.Now().Add(-1 * time.Minute),
	}
	err = txStore.Save(context.Background(), pendingTx)
	require.NoError(t, err)

	// Save nonce state
	savedNonce := originalNonce
	err = nonceStore.Save(context.Background(), &NonceState{
		Wallet:            fromAddr,
		ChainID:           testChainID,
		LocalPendingNonce: &savedNonce,
		ReservedNonces:    []uint64{originalNonce},
		UpdatedAt:         time.Now(),
	})
	require.NoError(t, err)

	// ========================================
	// Step 2: Create new WalletManager (simulating restart after crash)
	// ========================================

	// Track hook calls
	var recoveredTxs []*PendingTx
	var minedTxs []struct {
		tx      *PendingTx
		receipt *types.Receipt
	}
	var droppedTxs []*PendingTx
	hookMu := sync.Mutex{}

	// Track broadcast calls and what tx was broadcast
	var broadcastedTxs []*types.Transaction
	broadcastMu := sync.Mutex{}

	// Track monitor calls to verify gas bump behavior
	monitorCallCount := 0
	monitorMu := sync.Mutex{}

	// Create mock reader
	mockReader := &mockEthReader{
		GetMinedNonceFn: func(addr string) (uint64, error) {
			t.Logf("GetMinedNonce called for %s", addr)
			return originalNonce, nil // nonce 5 is still pending
		},
		GetPendingNonceFn: func(addr string) (uint64, error) {
			t.Logf("GetPendingNonce called for %s", addr)
			return originalNonce + 1, nil // next available nonce
		},
		SuggestedGasSettingsFn: func() (float64, float64, error) {
			t.Logf("SuggestedGasSettings called")
			return 25.0, 3.0, nil // Suggest higher gas prices
		},
		TxInfoFromHashFn: func(hash string) (TxInfo, error) {
			// Check if it's the original tx or a replacement
			if hash == signedTx.Hash().Hex() {
				// Original tx is still pending initially
				return TxInfo{Status: "pending"}, nil
			}
			// Any other hash (replacement tx) - check if it was broadcast
			broadcastMu.Lock()
			defer broadcastMu.Unlock()
			for _, tx := range broadcastedTxs {
				if tx.Hash().Hex() == hash {
					// Replacement tx is mined
					return TxInfo{
						Status: "done",
						Receipt: &types.Receipt{
							Status:      types.ReceiptStatusSuccessful,
							TxHash:      tx.Hash(),
							BlockNumber: big.NewInt(12345678),
							GasUsed:     21000,
						},
					}, nil
				}
			}
			return TxInfo{Status: "pending"}, nil
		},
		EthCallFn: func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
			t.Logf("EthCall (simulation) called: from=%s, to=%s", from, to)
			return nil, nil // Simulation passes
		},
	}

	// Create mock broadcaster that tracks all broadcasts
	mockBroadcaster := &mockEthBroadcaster{
		BroadcastTxFn: func(tx *types.Transaction) (string, bool, error) {
			t.Logf("BroadcastTx called for tx: %s, gasFeeCap: %v, gasTipCap: %v, nonce: %d",
				tx.Hash().Hex(), tx.GasFeeCap(), tx.GasTipCap(), tx.Nonce())
			broadcastMu.Lock()
			broadcastedTxs = append(broadcastedTxs, tx)
			broadcastMu.Unlock()
			return tx.Hash().Hex(), true, nil
		},
		BroadcastTxSyncFn: func(tx *types.Transaction) (*types.Receipt, error) {
			t.Logf("BroadcastTxSync called for tx: %s, gasFeeCap: %v, gasTipCap: %v, nonce: %d",
				tx.Hash().Hex(), tx.GasFeeCap(), tx.GasTipCap(), tx.Nonce())
			broadcastMu.Lock()
			broadcastedTxs = append(broadcastedTxs, tx)
			broadcastMu.Unlock()
			return &types.Receipt{
				Status:      types.ReceiptStatusSuccessful,
				TxHash:      tx.Hash(),
				BlockNumber: big.NewInt(12345678),
				GasUsed:     21000,
			}, nil
		},
	}

	// Create mock monitor that triggers the "slow" timeout path, then returns "done"
	// Important: MonitorTxContext has a 5 second hardcoded timeout that returns "slow".
	// The monitor must NOT respond within 5 seconds to trigger the "slow" path.
	// For the second call (after gas bump), we want it to return "done" immediately.
	mockMonitor := &mockTxMonitor{
		Delay: 6 * time.Second, // Longer than the 5 second timeout - triggers "slow" on first call
	}
	mockMonitor.StatusSequence = []TxMonitorStatus{
		{Status: "pending"}, // First call - ignored due to timeout, "slow" path fires
		{Status: "done", Receipt: &types.Receipt{ // Second call - replacement tx is mined
			Status:      types.ReceiptStatusSuccessful,
			BlockNumber: big.NewInt(12345678),
			GasUsed:     21000,
		}},
	}

	// Wrap monitor to track calls and reset delay after first call
	originalMonitor := mockMonitor
	monitorWrapper := func(r EthReader) TxMonitor {
		return &monitorInterceptor{
			underlying: originalMonitor,
			t:          t,
			onCall: func() {
				monitorMu.Lock()
				count := monitorCallCount
				monitorCallCount++
				// After first call, remove the delay so second call returns immediately
				if count == 0 {
					// Reset delay after first call is initiated (will take effect on next call)
					go func() {
						time.Sleep(100 * time.Millisecond) // Wait a bit for first call to start
						originalMonitor.mu.Lock()
						originalMonitor.Delay = 0
						originalMonitor.mu.Unlock()
					}()
				}
				monitorMu.Unlock()
				t.Logf("Monitor.MakeWaitChannelWithInterval called (call #%d)", count+1)
			},
		}
	}

	// Create the "recovered" WalletManager with same persistence stores
	// Use WithNetworkResolver to return our mock network for the test chain ID
	wm := NewWalletManager(
		WithNonceStore(nonceStore),
		WithTxStore(txStore),
		WithNetworkResolver(func(chainID uint64) (networks.Network, error) {
			if chainID == testChainID {
				return mockNetwork, nil
			}
			return nil, fmt.Errorf("unknown chain ID: %d", chainID)
		}),
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return mockReader, nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return mockBroadcaster, nil
		}),
		WithTxMonitorFactory(monitorWrapper),
	)

	// Register the wallet account so it can sign replacement transactions
	acc, err := account.NewPrivateKeyAccount("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	wm.SetAccount(acc)

	// Initialize network and verify no sync tx support
	_, err = wm.Reader(mockNetwork)
	require.NoError(t, err)
	t.Logf("mockNetwork.IsSyncTxSupported() = %v (should be false)", mockNetwork.IsSyncTxSupported())

	// ========================================
	// Step 3: Call Recover() with hooks
	// ========================================
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := RecoveryOptions{
		ResumeMonitoring:      true,
		TxCheckInterval:       10 * time.Millisecond, // Fast for testing
		MaxConcurrentMonitors: 5,
		OnTxRecovered: func(tx *PendingTx) {
			t.Logf("OnTxRecovered called for tx: %s", tx.Hash.Hex())
			hookMu.Lock()
			recoveredTxs = append(recoveredTxs, tx)
			hookMu.Unlock()
		},
		OnTxMined: func(tx *PendingTx, receipt *types.Receipt) {
			t.Logf("OnTxMined called for tx: %s, receipt status: %v", tx.Hash.Hex(), receipt.Status)
			hookMu.Lock()
			minedTxs = append(minedTxs, struct {
				tx      *PendingTx
				receipt *types.Receipt
			}{tx, receipt})
			hookMu.Unlock()
		},
		OnTxDropped: func(tx *PendingTx) {
			t.Logf("OnTxDropped called for tx: %s", tx.Hash.Hex())
			hookMu.Lock()
			droppedTxs = append(droppedTxs, tx)
			hookMu.Unlock()
		},
	}

	result, err := wm.RecoverWithOptions(ctx, opts)
	require.NoError(t, err)

	// Check for recovery errors immediately
	if len(result.Errors) > 0 {
		t.Logf("Recovery errors: %v", result.Errors)
	}
	t.Logf("Recovery result: RecoveredTxs=%d, MinedTxs=%d, DroppedTxs=%d, ReconciledNonces=%d",
		result.RecoveredTxs, result.MinedTxs, result.DroppedTxs, result.ReconciledNonces)

	// ========================================
	// Step 4: Wait for async recovery to complete
	// ========================================
	// Recovery launches goroutines, so we need to wait for them
	// Note: First monitor call will timeout after 5 seconds (triggering "slow"),
	// then gas bump happens, then second call returns immediately.
	// Total expected time: ~6 seconds
	require.Eventually(t, func() bool {
		hookMu.Lock()
		defer hookMu.Unlock()
		// Wait until OnTxMined is called (recovery complete) OR OnTxDropped
		return len(minedTxs) > 0 || len(droppedTxs) > 0
	}, 20*time.Second, 100*time.Millisecond, "OnTxMined or OnTxDropped should be called after recovery")

	// Check if any txs were dropped (indicates an error in resume process)
	hookMu.Lock()
	if len(droppedTxs) > 0 {
		t.Logf("Dropped txs: %d", len(droppedTxs))
		for i, tx := range droppedTxs {
			t.Logf("Dropped tx %d: %s", i, tx.Hash.Hex())
		}
	}
	hookMu.Unlock()

	// ========================================
	// Step 5: Verify recovery results
	// ========================================

	// Check RecoveryResult
	assert.Equal(t, 1, result.RecoveredTxs, "should have recovered 1 pending tx")
	assert.Equal(t, 0, result.MinedTxs, "tx was pending, not already mined")
	assert.Equal(t, 0, result.DroppedTxs, "tx was not dropped")
	assert.Equal(t, 1, result.ReconciledNonces, "should have reconciled 1 nonce state")

	// ========================================
	// Step 6: Verify OnTxRecovered hook was called
	// ========================================
	hookMu.Lock()
	assert.Len(t, recoveredTxs, 1, "OnTxRecovered should be called once")
	if len(recoveredTxs) > 0 {
		assert.Equal(t, signedTx.Hash(), recoveredTxs[0].Hash, "recovered tx should be the original")
	}
	hookMu.Unlock()

	// ========================================
	// Step 7: Verify gas bump happened - a replacement tx was broadcast
	// ========================================
	broadcastMu.Lock()
	require.GreaterOrEqual(t, len(broadcastedTxs), 1, "should have broadcast at least one replacement tx")
	replacementTx := broadcastedTxs[len(broadcastedTxs)-1]
	broadcastMu.Unlock()

	// Verify replacement tx has higher gas prices (bumped)
	originalGasPriceFloat := float64(originalGasFeeCap.Uint64()) / 1e9 // 22 gwei
	replacementGasPriceFloat := float64(replacementTx.GasFeeCap().Uint64()) / 1e9
	assert.Greater(t, replacementGasPriceFloat, originalGasPriceFloat,
		"replacement tx should have higher gas price (%.2f > %.2f)", replacementGasPriceFloat, originalGasPriceFloat)

	// Verify replacement tx uses same nonce (for replacement)
	assert.Equal(t, originalNonce, replacementTx.Nonce(), "replacement tx should use same nonce")

	// ========================================
	// Step 8: Verify original tx is marked as replaced in store
	// ========================================
	time.Sleep(100 * time.Millisecond) // Give async operations time to complete

	txStore.mu.RLock()
	originalTxInStore := txStore.txs[signedTx.Hash()]
	txStore.mu.RUnlock()

	require.NotNil(t, originalTxInStore, "original tx should still be in store")
	assert.Equal(t, PendingTxStatusReplaced, originalTxInStore.Status,
		"original tx should be marked as replaced")

	// ========================================
	// Step 9: Verify replacement tx is saved with mined status
	// ========================================
	txStore.mu.RLock()
	replacementTxInStore := txStore.txs[replacementTx.Hash()]
	txStore.mu.RUnlock()

	require.NotNil(t, replacementTxInStore, "replacement tx should be saved in store")
	assert.Equal(t, PendingTxStatusMined, replacementTxInStore.Status,
		"replacement tx should be marked as mined")
	assert.NotNil(t, replacementTxInStore.Receipt, "replacement tx should have receipt")

	// ========================================
	// Step 10: Verify OnTxMined hook was called with correct data
	// ========================================
	hookMu.Lock()
	assert.GreaterOrEqual(t, len(minedTxs), 1, "OnTxMined should be called at least once")
	if len(minedTxs) > 0 {
		lastMinedCall := minedTxs[len(minedTxs)-1]
		assert.NotNil(t, lastMinedCall.receipt, "receipt should be provided to OnTxMined")
		assert.Equal(t, types.ReceiptStatusSuccessful, lastMinedCall.receipt.Status,
			"receipt should show successful status")
	}
	hookMu.Unlock()

	// ========================================
	// Step 11: Verify monitor was called multiple times (slow -> done)
	// ========================================
	monitorMu.Lock()
	assert.GreaterOrEqual(t, monitorCallCount, 2, "monitor should be called at least twice (slow, then done)")
	monitorMu.Unlock()

	// ========================================
	// Step 12: Verify no txs were dropped
	// ========================================
	hookMu.Lock()
	assert.Len(t, droppedTxs, 0, "no txs should be dropped")
	hookMu.Unlock()
}

// monitorInterceptor wraps a TxMonitor to track calls and log status
type monitorInterceptor struct {
	underlying TxMonitor
	onCall     func()
	t          *testing.T
}

func (m *monitorInterceptor) MakeWaitChannelWithInterval(hash string, interval time.Duration) <-chan TxMonitorStatus {
	if m.onCall != nil {
		m.onCall()
	}
	ch := m.underlying.MakeWaitChannelWithInterval(hash, interval)

	// Wrap the channel to log the status
	wrappedCh := make(chan TxMonitorStatus, 1)
	go func() {
		status := <-ch
		if m.t != nil {
			m.t.Logf("Monitor returned status: %s for hash: %s", status.Status, hash)
		}
		wrappedCh <- status
		close(wrappedCh)
	}()
	return wrappedCh
}

// mockNetworkNoSyncTx is a mock network that doesn't support sync transactions.
// This allows testing the slow tx -> gas bump flow which only applies to networks
// without sync tx support (where transactions have a pending state to monitor).
type mockNetworkNoSyncTx struct {
	chainID uint64
	name    string
}

func newMockNetworkNoSyncTx(chainID uint64, name string) *mockNetworkNoSyncTx {
	return &mockNetworkNoSyncTx{chainID: chainID, name: name}
}

func (m *mockNetworkNoSyncTx) GetName() string                            { return m.name }
func (m *mockNetworkNoSyncTx) GetChainID() uint64                         { return m.chainID }
func (m *mockNetworkNoSyncTx) GetAlternativeNames() []string              { return nil }
func (m *mockNetworkNoSyncTx) GetNativeTokenSymbol() string               { return "ETH" }
func (m *mockNetworkNoSyncTx) GetNativeTokenDecimal() uint64              { return 18 }
func (m *mockNetworkNoSyncTx) GetBlockTime() time.Duration                { return 12 * time.Second }
func (m *mockNetworkNoSyncTx) GetNodeVariableName() string                { return "MOCK_NODE" }
func (m *mockNetworkNoSyncTx) GetDefaultNodes() map[string]string         { return nil }
func (m *mockNetworkNoSyncTx) GetBlockExplorerAPIKeyVariableName() string { return "" }
func (m *mockNetworkNoSyncTx) GetBlockExplorerAPIURL() string             { return "" }
func (m *mockNetworkNoSyncTx) RecommendedGasPrice() (float64, error)      { return 20.0, nil }
func (m *mockNetworkNoSyncTx) GetABIString(address string) (string, error) {
	return "", nil
}
func (m *mockNetworkNoSyncTx) IsSyncTxSupported() bool   { return false } // Key: no sync tx support
func (m *mockNetworkNoSyncTx) MultiCallContract() string { return "" }
func (m *mockNetworkNoSyncTx) MarshalJSON() ([]byte, error) {
	return []byte(`{"chainID":` + string(rune(m.chainID)) + `}`), nil
}
func (m *mockNetworkNoSyncTx) UnmarshalJSON([]byte) error { return nil }
