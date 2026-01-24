package walletarmy

import (
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/gethclient"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/util/account"
)

// ============================================================
// Mock Implementations
// ============================================================

// mockEthReader implements EthReader for testing
type mockEthReader struct {
	mu sync.Mutex

	// Function hooks - set these to customize behavior
	GetMinedNonceFn        func(addr string) (uint64, error)
	GetPendingNonceFn      func(addr string) (uint64, error)
	EstimateExactGasFn     func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error)
	SuggestedGasSettingsFn func() (float64, float64, error)
	EthCallFn              func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error)
	TxInfoFromHashFn       func(hash string) (TxInfo, error)

	// Call tracking for assertions
	GetMinedNonceCalls    []string
	GetPendingNonceCalls  []string
	EstimateExactGasCalls []struct {
		From, To string
		GasPrice float64
		Value    *big.Int
		Data     []byte
	}
	EthCallCalls []struct {
		From, To string
		Data     []byte
	}
	TxInfoFromHashCalls []string
}

func (m *mockEthReader) GetMinedNonce(addr string) (uint64, error) {
	m.mu.Lock()
	m.GetMinedNonceCalls = append(m.GetMinedNonceCalls, addr)
	m.mu.Unlock()
	if m.GetMinedNonceFn != nil {
		return m.GetMinedNonceFn(addr)
	}
	return 0, nil
}

func (m *mockEthReader) GetPendingNonce(addr string) (uint64, error) {
	m.mu.Lock()
	m.GetPendingNonceCalls = append(m.GetPendingNonceCalls, addr)
	m.mu.Unlock()
	if m.GetPendingNonceFn != nil {
		return m.GetPendingNonceFn(addr)
	}
	return 0, nil
}

func (m *mockEthReader) EstimateExactGas(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
	m.mu.Lock()
	m.EstimateExactGasCalls = append(m.EstimateExactGasCalls, struct {
		From, To string
		GasPrice float64
		Value    *big.Int
		Data     []byte
	}{from, to, gasPrice, value, data})
	m.mu.Unlock()
	if m.EstimateExactGasFn != nil {
		return m.EstimateExactGasFn(from, to, gasPrice, value, data)
	}
	return 21000, nil
}

func (m *mockEthReader) SuggestedGasSettings() (float64, float64, error) {
	if m.SuggestedGasSettingsFn != nil {
		return m.SuggestedGasSettingsFn()
	}
	return 20.0, 2.0, nil
}

func (m *mockEthReader) EthCall(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
	m.mu.Lock()
	m.EthCallCalls = append(m.EthCallCalls, struct {
		From, To string
		Data     []byte
	}{from, to, data})
	m.mu.Unlock()
	if m.EthCallFn != nil {
		return m.EthCallFn(from, to, data, overrides)
	}
	return nil, nil
}

func (m *mockEthReader) TxInfoFromHash(hash string) (TxInfo, error) {
	m.mu.Lock()
	m.TxInfoFromHashCalls = append(m.TxInfoFromHashCalls, hash)
	m.mu.Unlock()
	if m.TxInfoFromHashFn != nil {
		return m.TxInfoFromHashFn(hash)
	}
	return TxInfo{Status: "pending"}, nil
}

// mockEthBroadcaster implements EthBroadcaster for testing
type mockEthBroadcaster struct {
	mu sync.Mutex

	BroadcastTxFn     func(tx *types.Transaction) (string, bool, error)
	BroadcastTxSyncFn func(tx *types.Transaction) (*types.Receipt, error)

	BroadcastTxCalls     []*types.Transaction
	BroadcastTxSyncCalls []*types.Transaction
}

func (m *mockEthBroadcaster) BroadcastTx(tx *types.Transaction) (string, bool, error) {
	m.mu.Lock()
	m.BroadcastTxCalls = append(m.BroadcastTxCalls, tx)
	m.mu.Unlock()
	if m.BroadcastTxFn != nil {
		return m.BroadcastTxFn(tx)
	}
	return tx.Hash().Hex(), true, nil
}

func (m *mockEthBroadcaster) BroadcastTxSync(tx *types.Transaction) (*types.Receipt, error) {
	m.mu.Lock()
	m.BroadcastTxSyncCalls = append(m.BroadcastTxSyncCalls, tx)
	m.mu.Unlock()
	if m.BroadcastTxSyncFn != nil {
		return m.BroadcastTxSyncFn(tx)
	}
	return &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		TxHash:      tx.Hash(),
		BlockNumber: big.NewInt(12345),
		GasUsed:     21000,
	}, nil
}

// mockTxMonitor implements TxMonitor for testing
type mockTxMonitor struct {
	mu sync.Mutex

	StatusToReturn TxMonitorStatus
	Delay          time.Duration
	StatusSequence []TxMonitorStatus
	callCount      int

	MakeWaitChannelCalls []struct {
		Hash     string
		Interval time.Duration
	}
}

func (m *mockTxMonitor) MakeWaitChannelWithInterval(hash string, interval time.Duration) <-chan TxMonitorStatus {
	m.mu.Lock()
	m.MakeWaitChannelCalls = append(m.MakeWaitChannelCalls, struct {
		Hash     string
		Interval time.Duration
	}{hash, interval})

	var status TxMonitorStatus
	if len(m.StatusSequence) > 0 {
		if m.callCount < len(m.StatusSequence) {
			status = m.StatusSequence[m.callCount]
		} else {
			status = m.StatusSequence[len(m.StatusSequence)-1]
		}
		m.callCount++
	} else {
		status = m.StatusToReturn
	}
	delay := m.Delay
	m.mu.Unlock()

	ch := make(chan TxMonitorStatus, 1)
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		ch <- status
		close(ch)
	}()
	return ch
}

// ============================================================
// Test Fixtures
// ============================================================

var (
	testAddr1 = common.HexToAddress("0x1111111111111111111111111111111111111111")
	testAddr2 = common.HexToAddress("0x2222222222222222222222222222222222222222")
	testAddr3 = common.HexToAddress("0x3333333333333333333333333333333333333333")

	testPrivateKey1, _ = crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	oneEth      = big.NewInt(1000000000000000000)
	twentyGwei  = big.NewInt(20000000000)
	twoGwei     = big.NewInt(2000000000)
	chainIDMain = big.NewInt(1)
)

// ============================================================
// Transaction Builders
// ============================================================

func newTestDynamicTx(nonce uint64, to common.Address, value *big.Int, gasLimit uint64, gasTipCap, gasFeeCap *big.Int, chainID *big.Int) *types.Transaction {
	return types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &to,
		Value:     value,
		Data:      nil,
	})
}

func newTestTx(nonce uint64, to common.Address, value *big.Int) *types.Transaction {
	return newTestDynamicTx(nonce, to, value, 21000, twoGwei, twentyGwei, chainIDMain)
}

func newTestReceipt(tx *types.Transaction, status uint64) *types.Receipt {
	return &types.Receipt{
		Status:            status,
		TxHash:            tx.Hash(),
		BlockNumber:       big.NewInt(12345678),
		BlockHash:         common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
		TransactionIndex:  0,
		GasUsed:           tx.Gas(),
		CumulativeGasUsed: tx.Gas(),
	}
}

func newSuccessReceipt(tx *types.Transaction) *types.Receipt {
	return newTestReceipt(tx, types.ReceiptStatusSuccessful)
}

func newFailedReceipt(tx *types.Transaction) *types.Receipt {
	return newTestReceipt(tx, types.ReceiptStatusFailed)
}

// ============================================================
// Test Setup Helpers
// ============================================================

// testSetup contains all the mocks needed for a typical test
type testSetup struct {
	WM          *WalletManager
	Reader      *mockEthReader
	Broadcaster *mockEthBroadcaster
	Monitor     *mockTxMonitor
}

// newTestSetup creates a complete test setup with default mocks
func newTestSetup(t *testing.T) *testSetup {
	t.Helper()

	reader := &mockEthReader{
		GetMinedNonceFn:        func(addr string) (uint64, error) { return 0, nil },
		GetPendingNonceFn:      func(addr string) (uint64, error) { return 0, nil },
		SuggestedGasSettingsFn: func() (float64, float64, error) { return 20.0, 2.0, nil },
		EthCallFn: func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
			return nil, nil
		},
		TxInfoFromHashFn: func(hash string) (TxInfo, error) {
			return TxInfo{Status: "pending"}, nil
		},
	}

	broadcaster := &mockEthBroadcaster{}

	monitor := &mockTxMonitor{
		StatusToReturn: TxMonitorStatus{
			Status: "done",
		},
	}

	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return reader, nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return broadcaster, nil
		}),
		WithTxMonitorFactory(func(r EthReader) TxMonitor {
			return monitor
		}),
	)

	// Initialize the network to ensure all components are loaded
	_, err := wm.Reader(networks.EthereumMainnet)
	if err != nil {
		t.Fatalf("Failed to initialize network: %v", err)
	}

	return &testSetup{
		WM:          wm,
		Reader:      reader,
		Broadcaster: broadcaster,
		Monitor:     monitor,
	}
}

// ============================================================
// Mock Networks
// ============================================================

// mockNetwork is a configurable mock network for testing
type mockNetwork struct {
	chainID         uint64
	name            string
	syncTxSupported bool
	blockTime       time.Duration
	gasPrice        float64
}

func newMockNetwork(chainID uint64, name string, syncTxSupported bool) *mockNetwork {
	blockTime := 12 * time.Second
	gasPrice := 20.0
	if syncTxSupported {
		blockTime = 250 * time.Millisecond
		gasPrice = 0.1
	}
	return &mockNetwork{
		chainID:         chainID,
		name:            name,
		syncTxSupported: syncTxSupported,
		blockTime:       blockTime,
		gasPrice:        gasPrice,
	}
}

func (m *mockNetwork) GetName() string                            { return m.name }
func (m *mockNetwork) GetChainID() uint64                         { return m.chainID }
func (m *mockNetwork) GetAlternativeNames() []string              { return nil }
func (m *mockNetwork) GetNativeTokenSymbol() string               { return "ETH" }
func (m *mockNetwork) GetNativeTokenDecimal() uint64              { return 18 }
func (m *mockNetwork) GetBlockTime() time.Duration                { return m.blockTime }
func (m *mockNetwork) GetNodeVariableName() string                { return "MOCK_NODE" }
func (m *mockNetwork) GetDefaultNodes() map[string]string         { return nil }
func (m *mockNetwork) GetBlockExplorerAPIKeyVariableName() string { return "" }
func (m *mockNetwork) GetBlockExplorerAPIURL() string             { return "" }
func (m *mockNetwork) RecommendedGasPrice() (float64, error)      { return m.gasPrice, nil }
func (m *mockNetwork) GetABIString(address string) (string, error) {
	return "", nil
}
func (m *mockNetwork) IsSyncTxSupported() bool   { return m.syncTxSupported }
func (m *mockNetwork) MultiCallContract() string { return "" }
func (m *mockNetwork) MarshalJSON() ([]byte, error) {
	return []byte(`{"chainID":` + string(rune(m.chainID)) + `}`), nil
}
func (m *mockNetwork) UnmarshalJSON([]byte) error { return nil }

// ============================================================
// Sync Tx Test Setup
// ============================================================

// chainIDArbitrum is the chain ID for the mock Arbitrum network
var chainIDArbitrum = big.NewInt(42161)

// testPrivateKeyHex is the hex representation of the test private key
const testPrivateKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// syncTxTestSetup extends testSetup with sync tx network-specific fields
type syncTxTestSetup struct {
	*testSetup
	FromAddr common.Address // The actual address from the test account
}

// newTestSetupWithSyncNetwork creates a test setup with a network that supports sync tx
func newTestSetupWithSyncNetwork(t *testing.T) (*syncTxTestSetup, *mockNetwork) {
	t.Helper()

	reader := &mockEthReader{
		GetMinedNonceFn:        func(addr string) (uint64, error) { return 0, nil },
		GetPendingNonceFn:      func(addr string) (uint64, error) { return 0, nil },
		SuggestedGasSettingsFn: func() (float64, float64, error) { return 0.1, 0.01, nil },
		EthCallFn: func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
			return nil, nil
		},
		TxInfoFromHashFn: func(hash string) (TxInfo, error) {
			return TxInfo{Status: "pending"}, nil
		},
	}

	broadcaster := &mockEthBroadcaster{}

	monitor := &mockTxMonitor{
		StatusToReturn: TxMonitorStatus{
			Status: "done",
		},
	}

	mockNet := newMockNetwork(42161, "mock-arbitrum", true)

	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return reader, nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return broadcaster, nil
		}),
		WithTxMonitorFactory(func(r EthReader) TxMonitor {
			return monitor
		}),
	)

	// Set up a test account using the testPrivateKey1 hex
	acc, err := account.NewPrivateKeyAccount(testPrivateKeyHex)
	if err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}
	wm.SetAccount(acc)

	// Initialize the network
	_, err = wm.Reader(mockNet)
	if err != nil {
		t.Fatalf("Failed to initialize network: %v", err)
	}

	return &syncTxTestSetup{
		testSetup: &testSetup{
			WM:          wm,
			Reader:      reader,
			Broadcaster: broadcaster,
			Monitor:     monitor,
		},
		FromAddr: acc.Address(),
	}, mockNet
}

// newTestTxWithChainID creates a test transaction with a specific chain ID
func newTestTxWithChainID(nonce uint64, to common.Address, value *big.Int, chainID *big.Int) *types.Transaction {
	return newTestDynamicTx(nonce, to, value, 21000, twoGwei, twentyGwei, chainID)
}
