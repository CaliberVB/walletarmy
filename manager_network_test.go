package walletarmy

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient/gethclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tranvictor/jarvis/networks"

	"github.com/tranvictor/walletarmy/internal/circuitbreaker"
)

// Test network for consistent testing
var testNetwork = networks.EthereumMainnet

// createDefaultMockReader creates a mock reader with default implementations
func createDefaultMockReader() *mockEthReader {
	return &mockEthReader{
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
}

func TestReader_InitializesNetwork_WhenNotExists(t *testing.T) {
	setup := newTestSetup(t)

	reader, err := setup.WM.Reader(testNetwork)

	require.NoError(t, err)
	assert.NotNil(t, reader)
	// Second call should return cached reader
	reader2, err := setup.WM.Reader(testNetwork)
	require.NoError(t, err)
	assert.Equal(t, reader, reader2)
}

func TestReader_ReturnsError_WhenFactoryFails(t *testing.T) {
	factoryErr := errors.New("factory failed")
	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return nil, factoryErr
		}),
	)

	reader, err := wm.Reader(testNetwork)

	assert.Nil(t, reader)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't init reader")
}

func TestReader_CircuitBreakerOpen_ReturnsError_NetworkTest(t *testing.T) {
	setup := newTestSetup(t)

	// Force circuit breaker open by recording many failures
	cb := setup.WM.getCircuitBreaker(testNetwork.GetChainID())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	reader, err := setup.WM.Reader(testNetwork)

	assert.Nil(t, reader)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCircuitBreakerOpen)
}

func TestBroadcaster_InitializesNetwork_WhenNotExists(t *testing.T) {
	setup := newTestSetup(t)

	broadcaster, err := setup.WM.Broadcaster(testNetwork)

	require.NoError(t, err)
	assert.NotNil(t, broadcaster)
	// Second call should return cached broadcaster
	broadcaster2, err := setup.WM.Broadcaster(testNetwork)
	require.NoError(t, err)
	assert.Equal(t, broadcaster, broadcaster2)
}

func TestBroadcaster_ReturnsError_WhenFactoryFails(t *testing.T) {
	factoryErr := errors.New("broadcaster factory failed")
	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return createDefaultMockReader(), nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return nil, factoryErr
		}),
	)

	broadcaster, err := wm.Broadcaster(testNetwork)

	assert.Nil(t, broadcaster)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't init broadcaster")
}

func TestBroadcaster_CircuitBreakerOpen_ReturnsError(t *testing.T) {
	setup := newTestSetup(t)

	// Force circuit breaker open
	cb := setup.WM.getCircuitBreaker(testNetwork.GetChainID())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	broadcaster, err := setup.WM.Broadcaster(testNetwork)

	assert.Nil(t, broadcaster)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCircuitBreakerOpen)
}

func TestAnalyzer_InitializesNetwork_WhenNotExists(t *testing.T) {
	setup := newTestSetup(t)

	analyzer, err := setup.WM.Analyzer(testNetwork)

	require.NoError(t, err)
	assert.NotNil(t, analyzer)
	// Second call should return cached analyzer
	analyzer2, err := setup.WM.Analyzer(testNetwork)
	require.NoError(t, err)
	assert.Equal(t, analyzer, analyzer2)
}

func TestAnalyzer_CircuitBreakerOpen_ReturnsError(t *testing.T) {
	setup := newTestSetup(t)

	// Force circuit breaker open
	cb := setup.WM.getCircuitBreaker(testNetwork.GetChainID())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	analyzer, err := setup.WM.Analyzer(testNetwork)

	assert.Nil(t, analyzer)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCircuitBreakerOpen)
}

func TestGasSetting_FetchesFromReader_WhenNotCached(t *testing.T) {
	setup := newTestSetup(t)
	setup.Reader.SuggestedGasSettingsFn = func() (float64, float64, error) {
		return 50.0, 2.0, nil // 50 gwei gas price, 2 gwei tip cap
	}

	gasInfo, err := setup.WM.GasSetting(testNetwork)

	require.NoError(t, err)
	assert.NotNil(t, gasInfo)
	assert.Equal(t, 50.0, gasInfo.GasPrice)
	assert.Equal(t, 2.0, gasInfo.MaxPriorityPrice)
	assert.Equal(t, 50.0, gasInfo.FeePerGas)
}

func TestGasSetting_ReturnsCached_WhenNotStale(t *testing.T) {
	setup := newTestSetup(t)
	callCount := 0
	setup.Reader.SuggestedGasSettingsFn = func() (float64, float64, error) {
		callCount++
		return 50.0, 2.0, nil
	}

	// First call
	gasInfo1, err := setup.WM.GasSetting(testNetwork)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	// Second call (should be cached)
	gasInfo2, err := setup.WM.GasSetting(testNetwork)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount) // Should not have called reader again
	assert.Equal(t, gasInfo1.GasPrice, gasInfo2.GasPrice)
}

func TestGasSetting_RefreshesAfterTTL(t *testing.T) {
	setup := newTestSetup(t)
	callCount := 0
	setup.Reader.SuggestedGasSettingsFn = func() (float64, float64, error) {
		callCount++
		return float64(50 + callCount*10), 2.0, nil
	}

	// First call
	gasInfo1, err := setup.WM.GasSetting(testNetwork)
	require.NoError(t, err)
	assert.Equal(t, 60.0, gasInfo1.GasPrice) // 50 + 10

	// Manually set old timestamp to simulate TTL expiry
	gasInfo1.Timestamp = time.Now().Add(-GAS_INFO_TTL - time.Second)
	setup.WM.setGasInfo(testNetwork, gasInfo1)

	// Second call (should refresh because TTL expired)
	gasInfo2, err := setup.WM.GasSetting(testNetwork)
	require.NoError(t, err)
	assert.Equal(t, 2, callCount)
	assert.Equal(t, 70.0, gasInfo2.GasPrice) // 50 + 20
}

func TestGasSetting_ReturnsError_WhenReaderFails(t *testing.T) {
	setup := newTestSetup(t)
	setup.Reader.SuggestedGasSettingsFn = func() (float64, float64, error) {
		return 0, 0, errors.New("RPC error")
	}

	gasInfo, err := setup.WM.GasSetting(testNetwork)

	assert.Nil(t, gasInfo)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't get gas settings")
}

func TestRecordNetworkSuccess_UpdatesCircuitBreaker(t *testing.T) {
	setup := newTestSetup(t)

	// Record some failures first
	cb := setup.WM.getCircuitBreaker(testNetwork.GetChainID())
	cb.RecordFailure()
	cb.RecordFailure()

	// Record success
	setup.WM.RecordNetworkSuccess(testNetwork)

	statsAfter := setup.WM.GetCircuitBreakerStats(testNetwork)
	// After success, consecutive failures should be reset to 0
	assert.Equal(t, 0, statsAfter.ConsecutiveFailures)
	assert.Greater(t, statsAfter.ConsecutiveSuccesses, 0)
}

func TestRecordNetworkFailure_UpdatesCircuitBreaker(t *testing.T) {
	setup := newTestSetup(t)

	statsBefore := setup.WM.GetCircuitBreakerStats(testNetwork)
	failuresBefore := statsBefore.ConsecutiveFailures

	setup.WM.RecordNetworkFailure(testNetwork)

	statsAfter := setup.WM.GetCircuitBreakerStats(testNetwork)
	assert.Equal(t, failuresBefore+1, statsAfter.ConsecutiveFailures)
}

func TestGetCircuitBreakerStats_ReturnsStats(t *testing.T) {
	setup := newTestSetup(t)

	stats := setup.WM.GetCircuitBreakerStats(testNetwork)

	assert.Equal(t, circuitbreaker.StateClosed, stats.State)
	assert.Equal(t, 0, stats.ConsecutiveFailures)
}

func TestResetCircuitBreaker_ResetsState(t *testing.T) {
	setup := newTestSetup(t)

	// Record failures to change state
	for i := 0; i < 10; i++ {
		setup.WM.RecordNetworkFailure(testNetwork)
	}

	// Reset
	setup.WM.ResetCircuitBreaker(testNetwork)

	stats := setup.WM.GetCircuitBreakerStats(testNetwork)
	assert.Equal(t, circuitbreaker.StateClosed, stats.State)
	assert.Equal(t, 0, stats.ConsecutiveFailures)
}

func TestSetTx_StoresTransaction(t *testing.T) {
	setup := newTestSetup(t)
	wallet := testAddr1
	tx := newTestTx(5, testAddr3, oneEth)

	setup.WM.setTx(wallet, testNetwork, tx)

	// Verify the transaction was stored
	txMapRaw, ok := setup.WM.txs.Load(wallet)
	require.True(t, ok)
	txMap := txMapRaw.(map[uint64]map[uint64]*types.Transaction)
	storedTx := txMap[testNetwork.GetChainID()][5]
	assert.Equal(t, tx.Hash(), storedTx.Hash())
}

func TestSetTx_StoresMultipleTransactions(t *testing.T) {
	setup := newTestSetup(t)
	wallet := testAddr1
	tx1 := newTestTx(1, testAddr3, oneEth)
	tx2 := newTestTx(2, testAddr3, oneEth)
	tx3 := newTestTx(3, testAddr3, oneEth)

	setup.WM.setTx(wallet, testNetwork, tx1)
	setup.WM.setTx(wallet, testNetwork, tx2)
	setup.WM.setTx(wallet, testNetwork, tx3)

	txMapRaw, ok := setup.WM.txs.Load(wallet)
	require.True(t, ok)
	txMap := txMapRaw.(map[uint64]map[uint64]*types.Transaction)
	networkTxs := txMap[testNetwork.GetChainID()]
	assert.Len(t, networkTxs, 3)
	assert.Equal(t, tx1.Hash(), networkTxs[1].Hash())
	assert.Equal(t, tx2.Hash(), networkTxs[2].Hash())
	assert.Equal(t, tx3.Hash(), networkTxs[3].Hash())
}

func TestSetTx_MultipleNetworks(t *testing.T) {
	setup := newTestSetup(t)
	wallet := testAddr1
	network1 := networks.EthereumMainnet
	network2 := networks.BSCMainnet

	tx1 := newTestTx(1, testAddr3, oneEth)
	tx2 := newTestTx(1, testAddr3, oneEth) // Same nonce, different network

	setup.WM.setTx(wallet, network1, tx1)
	setup.WM.setTx(wallet, network2, tx2)

	txMapRaw, ok := setup.WM.txs.Load(wallet)
	require.True(t, ok)
	txMap := txMapRaw.(map[uint64]map[uint64]*types.Transaction)

	assert.NotNil(t, txMap[network1.GetChainID()])
	assert.NotNil(t, txMap[network2.GetChainID()])
}

func TestSetTx_MultipleWallets(t *testing.T) {
	setup := newTestSetup(t)
	wallet1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	wallet2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	tx1 := newTestTx(1, testAddr3, oneEth)
	tx2 := newTestTx(1, testAddr3, oneEth)

	setup.WM.setTx(wallet1, testNetwork, tx1)
	setup.WM.setTx(wallet2, testNetwork, tx2)

	txMap1Raw, ok := setup.WM.txs.Load(wallet1)
	require.True(t, ok)
	txMap2Raw, ok := setup.WM.txs.Load(wallet2)
	require.True(t, ok)

	txMap1 := txMap1Raw.(map[uint64]map[uint64]*types.Transaction)
	txMap2 := txMap2Raw.(map[uint64]map[uint64]*types.Transaction)

	assert.NotNil(t, txMap1[testNetwork.GetChainID()][1])
	assert.NotNil(t, txMap2[testNetwork.GetChainID()][1])
}

func TestInitNetwork_OnlyInitializesOnce(t *testing.T) {
	readerCallCount := 0
	broadcasterCallCount := 0

	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			readerCallCount++
			return createDefaultMockReader(), nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			broadcasterCallCount++
			return &mockEthBroadcaster{}, nil
		}),
		WithTxMonitorFactory(func(reader EthReader) TxMonitor {
			return &mockTxMonitor{}
		}),
	)

	// First call
	_, err := wm.Reader(testNetwork)
	require.NoError(t, err)

	// Second call (should use cached)
	_, err = wm.Reader(testNetwork)
	require.NoError(t, err)

	// Third call (should use cached)
	_, err = wm.Broadcaster(testNetwork)
	require.NoError(t, err)

	assert.Equal(t, 1, readerCallCount)
	assert.Equal(t, 1, broadcasterCallCount)
}

func TestGetGasSettingInfo_ReturnsNil_WhenNotSet(t *testing.T) {
	wm := NewWalletManager()

	gasInfo := wm.getGasSettingInfo(testNetwork)

	assert.Nil(t, gasInfo)
}

func TestSetGasInfo_StoresInfo(t *testing.T) {
	wm := NewWalletManager()
	info := &GasInfo{
		GasPrice:         100.0,
		MaxPriorityPrice: 5.0,
		FeePerGas:        100.0,
		Timestamp:        time.Now(),
	}

	wm.setGasInfo(testNetwork, info)

	retrieved := wm.getGasSettingInfo(testNetwork)
	assert.NotNil(t, retrieved)
	assert.Equal(t, 100.0, retrieved.GasPrice)
}

func TestDifferentNetworks_HaveSeparateCircuitBreakers(t *testing.T) {
	wm := NewWalletManager()
	network1 := networks.EthereumMainnet
	network2 := networks.BSCMainnet

	// Record failures only on network1
	for i := 0; i < 5; i++ {
		wm.RecordNetworkFailure(network1)
	}

	stats1 := wm.GetCircuitBreakerStats(network1)
	stats2 := wm.GetCircuitBreakerStats(network2)

	assert.Equal(t, 5, stats1.ConsecutiveFailures)
	assert.Equal(t, 0, stats2.ConsecutiveFailures)
}

func TestGasSetting_CircuitBreakerOpen_ReturnsError(t *testing.T) {
	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return createDefaultMockReader(), nil
		}),
	)

	// Force circuit breaker open
	cb := wm.getCircuitBreaker(testNetwork.GetChainID())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	gasInfo, err := wm.GasSetting(testNetwork)

	assert.Nil(t, gasInfo)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't get reader for gas settings")
}

func TestGetTxMonitor_ReturnsNil_WhenNotInitialized(t *testing.T) {
	wm := NewWalletManager()

	monitor := wm.getTxMonitor(testNetwork)

	assert.Nil(t, monitor)
}

func TestGetTxMonitor_ReturnsMonitor_WhenInitialized(t *testing.T) {
	mockMonitor := &mockTxMonitor{}
	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return createDefaultMockReader(), nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return &mockEthBroadcaster{}, nil
		}),
		WithTxMonitorFactory(func(reader EthReader) TxMonitor {
			return mockMonitor
		}),
	)

	// Initialize network by calling Reader
	_, err := wm.Reader(testNetwork)
	require.NoError(t, err)

	monitor := wm.getTxMonitor(testNetwork)

	assert.Equal(t, mockMonitor, monitor)
}

func TestInitNetwork_WithNilTxMonitorFactory(t *testing.T) {
	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return createDefaultMockReader(), nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return &mockEthBroadcaster{}, nil
		}),
		WithTxMonitorFactory(func(reader EthReader) TxMonitor {
			return nil // Factory returns nil
		}),
	)

	// Should not panic, just skip storing nil monitor
	_, err := wm.Reader(testNetwork)
	require.NoError(t, err)

	monitor := wm.getTxMonitor(testNetwork)
	assert.Nil(t, monitor)
}

func TestGasInfo_TTL_Constant(t *testing.T) {
	// Verify the TTL is set to a reasonable value
	assert.Equal(t, 60*time.Second, GAS_INFO_TTL)
}

func TestCircuitBreaker_RecordsSuccessOnInit(t *testing.T) {
	setup := newTestSetup(t)

	// Record a failure first
	setup.WM.RecordNetworkFailure(testNetwork)

	// Reader call should record success
	_, err := setup.WM.Reader(testNetwork)
	require.NoError(t, err)

	statsAfter := setup.WM.GetCircuitBreakerStats(testNetwork)
	// After success, consecutive failures should be reset
	assert.Equal(t, 0, statsAfter.ConsecutiveFailures)
}

func TestSetTx_OverwritesSameNonce(t *testing.T) {
	setup := newTestSetup(t)
	wallet := testAddr1

	tx1 := newTestTx(1, testAddr3, big.NewInt(100))
	tx2 := newTestTx(1, testAddr3, big.NewInt(200)) // Same nonce, different value

	setup.WM.setTx(wallet, testNetwork, tx1)
	setup.WM.setTx(wallet, testNetwork, tx2)

	txMapRaw, ok := setup.WM.txs.Load(wallet)
	require.True(t, ok)
	txMap := txMapRaw.(map[uint64]map[uint64]*types.Transaction)

	// Should have overwritten with tx2
	storedTx := txMap[testNetwork.GetChainID()][1]
	assert.Equal(t, tx2.Hash(), storedTx.Hash())
}
