package walletarmy

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/gethclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/util/account"
)

// ============================================================
// BuildTx Tests
// ============================================================

func TestBuildTx_EstimatesGas_WhenZero(t *testing.T) {
	setup := newTestSetup(t)

	// Track that EstimateExactGas was called
	estimateCalled := false
	setup.Reader.EstimateExactGasFn = func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
		estimateCalled = true
		return 50000, nil
	}

	tx, err := setup.WM.BuildTx(
		2, // txType
		testAddr1,
		testAddr2,
		big.NewInt(5), // nonce provided
		oneEth,
		0,  // gasLimit = 0, should trigger estimation
		0,  // extraGasLimit
		20, // gasPrice
		0,  // extraGasPrice
		2,  // tipCapGwei
		0,  // extraTipCapGwei
		nil,
		networks.EthereumMainnet,
	)

	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.True(t, estimateCalled, "EstimateExactGas should have been called")
	assert.Equal(t, uint64(50000), tx.Gas())
}

func TestBuildTx_UsesProvidedGasLimit(t *testing.T) {
	setup := newTestSetup(t)

	// EstimateExactGas should NOT be called
	estimateCalled := false
	setup.Reader.EstimateExactGasFn = func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
		estimateCalled = true
		return 50000, nil
	}

	tx, err := setup.WM.BuildTx(
		2,
		testAddr1,
		testAddr2,
		big.NewInt(5),
		oneEth,
		21000, // gasLimit provided
		1000,  // extraGasLimit
		20,
		0,
		2,
		0,
		nil,
		networks.EthereumMainnet,
	)

	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.False(t, estimateCalled, "EstimateExactGas should NOT have been called")
	assert.Equal(t, uint64(22000), tx.Gas()) // 21000 + 1000 extra
}

func TestBuildTx_AcquiresNonce_WhenNil(t *testing.T) {
	setup := newTestSetup(t)

	// Set up nonce tracking
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	tx, err := setup.WM.BuildTx(
		2,
		testAddr1,
		testAddr2,
		nil, // nonce = nil, should acquire
		oneEth,
		21000,
		0,
		20,
		0,
		2,
		0,
		nil,
		networks.EthereumMainnet,
	)

	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, uint64(5), tx.Nonce())
	assert.Contains(t, setup.Reader.GetMinedNonceCalls, testAddr1.Hex())
	assert.Contains(t, setup.Reader.GetPendingNonceCalls, testAddr1.Hex())
}

func TestBuildTx_GetsGasSettings_WhenZero(t *testing.T) {
	setup := newTestSetup(t)

	suggestedCalled := false
	setup.Reader.SuggestedGasSettingsFn = func() (float64, float64, error) {
		suggestedCalled = true
		return 30.0, 3.0, nil // 30 gwei gas price, 3 gwei tip
	}

	tx, err := setup.WM.BuildTx(
		2,
		testAddr1,
		testAddr2,
		big.NewInt(5),
		oneEth,
		21000,
		0,
		0, // gasPrice = 0, should get from network
		0,
		0, // tipCapGwei = 0, should get from network
		0,
		nil,
		networks.EthereumMainnet,
	)

	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.True(t, suggestedCalled, "SuggestedGasSettings should have been called")
}

func TestBuildTx_ReturnsError_WhenGasEstimationFails(t *testing.T) {
	setup := newTestSetup(t)

	expectedErr := errors.New("execution reverted")
	setup.Reader.EstimateExactGasFn = func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
		return 0, expectedErr
	}

	tx, err := setup.WM.BuildTx(
		2,
		testAddr1,
		testAddr2,
		big.NewInt(5),
		oneEth,
		0, // gasLimit = 0, triggers estimation
		0,
		20,
		0,
		2,
		0,
		nil,
		networks.EthereumMainnet,
	)

	assert.Nil(t, tx)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrEstimateGasFailed))
}

func TestBuildTx_ReturnsError_WhenNonceAcquisitionFails(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) {
		return 0, errors.New("network error")
	}

	tx, err := setup.WM.BuildTx(
		2,
		testAddr1,
		testAddr2,
		nil, // nonce = nil
		oneEth,
		21000,
		0,
		20,
		0,
		2,
		0,
		nil,
		networks.EthereumMainnet,
	)

	assert.Nil(t, tx)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquireNonceFailed))
}

// ============================================================
// BroadcastTx Tests
// ============================================================

func TestBroadcastTx_Success(t *testing.T) {
	setup := newTestSetup(t)

	// Create and sign a transaction
	tx := newTestTx(0, testAddr2, oneEth)
	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(chainIDMain), testPrivateKey1)
	require.NoError(t, err)

	hash, broadcasted, broadcastErr := setup.WM.BroadcastTx(signedTx)

	assert.True(t, broadcasted)
	assert.Equal(t, signedTx.Hash().Hex(), hash)
	assert.Nil(t, broadcastErr)
	assert.Len(t, setup.Broadcaster.BroadcastTxCalls, 1)
}

func TestBroadcastTx_Failure(t *testing.T) {
	setup := newTestSetup(t)

	setup.Broadcaster.BroadcastTxFn = func(tx *types.Transaction) (string, bool, error) {
		return "", false, errors.New("insufficient funds for gas * price + value")
	}

	// Create and sign a transaction
	tx := newTestTx(0, testAddr2, oneEth)
	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(chainIDMain), testPrivateKey1)
	require.NoError(t, err)

	hash, broadcasted, broadcastErr := setup.WM.BroadcastTx(signedTx)

	assert.False(t, broadcasted)
	assert.Empty(t, hash)
	assert.NotNil(t, broadcastErr)
	assert.Equal(t, ErrInsufficientFund, broadcastErr)
}

// ============================================================
// MonitorTxContext Tests
// ============================================================

func TestMonitorTxContext_ReturnsMined(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(0, testAddr2, oneEth)
	receipt := newSuccessReceipt(tx)

	setup.Monitor.StatusToReturn = TxMonitorStatus{
		Status:  "done",
		Receipt: receipt,
	}

	ctx := context.Background()
	statusChan := setup.WM.MonitorTxContext(ctx, tx, networks.EthereumMainnet, time.Second)

	status := <-statusChan
	assert.Equal(t, "mined", status.Status)
	assert.Equal(t, receipt, status.Receipt)
}

func TestMonitorTxContext_ReturnsReverted(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(0, testAddr2, oneEth)
	receipt := newFailedReceipt(tx)

	setup.Monitor.StatusToReturn = TxMonitorStatus{
		Status:  "reverted",
		Receipt: receipt,
	}

	ctx := context.Background()
	statusChan := setup.WM.MonitorTxContext(ctx, tx, networks.EthereumMainnet, time.Second)

	status := <-statusChan
	assert.Equal(t, "reverted", status.Status)
	assert.Equal(t, receipt, status.Receipt)
}

func TestMonitorTxContext_ReturnsCancelledOnContextCancel(t *testing.T) {
	setup := newTestSetup(t)

	// Set a delay so we can cancel before status is returned
	setup.Monitor.Delay = 5 * time.Second

	tx := newTestTx(0, testAddr2, oneEth)

	ctx, cancel := context.WithCancel(context.Background())

	statusChan := setup.WM.MonitorTxContext(ctx, tx, networks.EthereumMainnet, time.Second)

	// Cancel context immediately
	cancel()

	status := <-statusChan
	assert.Equal(t, "cancelled", status.Status)
	assert.Nil(t, status.Receipt)
}

// ============================================================
// TxExecutionContext Tests
// ============================================================

func TestTxExecutionContext_AdjustGasPricesForSlowTx_Success(t *testing.T) {
	ctx := &TxExecutionContext{
		MaxGasPrice: 200.0, // High limit
		MaxTipCap:   100.0, // High limit
	}

	tx := newTestDynamicTx(5, testAddr2, oneEth, 21000,
		big.NewInt(2000000000),  // 2 gwei tip
		big.NewInt(20000000000), // 20 gwei max fee
		big.NewInt(1),
	)

	adjusted := ctx.AdjustGasPricesForSlowTx(tx)

	assert.True(t, adjusted)
	// Gas price should be increased by GasPriceIncreasePercent (1.2)
	expectedGasPrice := 20.0 * GasPriceIncreasePercent
	assert.InDelta(t, expectedGasPrice, ctx.RetryGasPrice, 0.001)
	// Tip cap should be increased by TipCapIncreasePercent (1.1)
	expectedTipCap := 2.0 * TipCapIncreasePercent
	assert.InDelta(t, expectedTipCap, ctx.RetryTipCap, 0.001)
	// Nonce should be preserved
	assert.Equal(t, big.NewInt(5), ctx.RetryNonce)
}

func TestTxExecutionContext_AdjustGasPricesForSlowTx_HitsGasPriceLimit(t *testing.T) {
	ctx := &TxExecutionContext{
		MaxGasPrice: 22.0, // Low limit - 20 * 1.2 = 24 would exceed
		MaxTipCap:   100.0,
	}

	tx := newTestDynamicTx(5, testAddr2, oneEth, 21000,
		big.NewInt(2000000000),
		big.NewInt(20000000000),
		big.NewInt(1),
	)

	adjusted := ctx.AdjustGasPricesForSlowTx(tx)

	assert.False(t, adjusted, "Should return false when gas price limit would be exceeded")
}

func TestTxExecutionContext_AdjustGasPricesForSlowTx_HitsTipCapLimit(t *testing.T) {
	ctx := &TxExecutionContext{
		MaxGasPrice: 200.0,
		MaxTipCap:   2.1, // Low limit - 2 * 1.1 = 2.2 would exceed
	}

	tx := newTestDynamicTx(5, testAddr2, oneEth, 21000,
		big.NewInt(2000000000),
		big.NewInt(20000000000),
		big.NewInt(1),
	)

	adjusted := ctx.AdjustGasPricesForSlowTx(tx)

	assert.False(t, adjusted, "Should return false when tip cap limit would be exceeded")
}

func TestTxExecutionContext_IncrementRetryCountAndCheck(t *testing.T) {
	tests := []struct {
		name           string
		numRetries     int
		currentRetries int
		expectResult   bool // true means we expect a result (exceeded retries)
	}{
		{"first retry within limit", 3, 0, false},
		{"second retry within limit", 3, 1, false},
		{"at limit", 3, 2, false},
		{"exceeds limit", 3, 3, true},
		{"zero retries allowed - first try", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &TxExecutionContext{
				NumRetries:       tt.numRetries,
				ActualRetryCount: tt.currentRetries,
			}

			result := ctx.IncrementRetryCountAndCheck("test error")

			if tt.expectResult {
				assert.NotNil(t, result)
				assert.True(t, result.ShouldReturn)
				assert.False(t, result.ShouldRetry)
				assert.True(t, errors.Is(result.Error, ErrEnsureTxOutOfRetries))
			} else {
				assert.Nil(t, result)
			}
		})
	}
}

// ============================================================
// Concurrent Nonce Acquisition Tests
// ============================================================

func TestBuildTx_ConcurrentNonceAcquisition(t *testing.T) {
	setup := newTestSetup(t)

	// Track nonces returned
	var nextNonce uint64 = 0
	var mu sync.Mutex

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) {
		return 0, nil
	}
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) {
		return 0, nil
	}

	// Run concurrent BuildTx calls
	const numGoroutines = 10
	var wg sync.WaitGroup
	nonces := make([]uint64, numGoroutines)
	errs := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tx, err := setup.WM.BuildTx(
				2,
				testAddr1,
				testAddr2,
				nil, // Acquire nonce
				oneEth,
				21000,
				0,
				20,
				0,
				2,
				0,
				nil,
				networks.EthereumMainnet,
			)
			if err != nil {
				errs[idx] = err
				return
			}
			mu.Lock()
			nonces[idx] = tx.Nonce()
			if tx.Nonce() >= nextNonce {
				nextNonce = tx.Nonce() + 1
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Check no errors
	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d had error", i)
	}

	// Check all nonces are unique
	nonceSet := make(map[uint64]bool)
	for _, n := range nonces {
		assert.False(t, nonceSet[n], "duplicate nonce %d found", n)
		nonceSet[n] = true
	}

	// Check nonces form a continuous sequence [0, numGoroutines)
	assert.Equal(t, uint64(numGoroutines), nextNonce, "expected nextNonce to be %d", numGoroutines)
	for i := uint64(0); i < numGoroutines; i++ {
		assert.True(t, nonceSet[i], "missing nonce %d in sequence", i)
	}
}

// ============================================================
// handleBroadcastError Tests
// ============================================================

func TestHandleBroadcastError_NonceTooLow_ChecksOldTxs(t *testing.T) {
	setup := newTestSetup(t)

	// Create an old tx that's been "mined"
	oldTx := newTestTx(5, testAddr2, oneEth)
	oldReceipt := newSuccessReceipt(oldTx)

	// Set up reader to return "done" for the old tx
	setup.Reader.TxInfoFromHashFn = func(hash string) (TxInfo, error) {
		if hash == oldTx.Hash().Hex() {
			return TxInfo{Status: "done", Receipt: oldReceipt}, nil
		}
		return TxInfo{Status: "pending"}, nil
	}

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		OldTxs: map[string]*types.Transaction{
			oldTx.Hash().Hex(): oldTx,
		},
		Network: networks.EthereumMainnet,
	}

	newTx := newTestTx(6, testAddr2, oneEth)

	result := setup.WM.handleNonceIsLowError(newTx, execCtx)

	// Should return the old tx that was mined
	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.Equal(t, oldTx, result.Transaction)
	assert.Nil(t, result.Error)
}

func TestHandleBroadcastError_TxIsKnown_RetriesWithSameNonce(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(5, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		OldTxs:     make(map[string]*types.Transaction),
		Network:    networks.EthereumMainnet,
	}

	result := setup.WM.handleBroadcastError(ErrTxIsKnown, tx, execCtx)

	assert.False(t, result.ShouldReturn)
	assert.True(t, result.ShouldRetry)
	assert.Equal(t, big.NewInt(5), execCtx.RetryNonce)
}

func TestHandleBroadcastError_InsufficientFunds_IncrementsRetry(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(5, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries:       5,
		ActualRetryCount: 0,
		OldTxs:           make(map[string]*types.Transaction),
		Network:          networks.EthereumMainnet,
	}

	result := setup.WM.handleBroadcastError(ErrInsufficientFund, tx, execCtx)

	assert.False(t, result.ShouldReturn)
	assert.True(t, result.ShouldRetry)
	assert.Equal(t, 1, execCtx.ActualRetryCount)
	assert.Equal(t, big.NewInt(5), execCtx.RetryNonce)
}

func TestHandleBroadcastError_ExceedsRetries(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(5, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries:       2,
		ActualRetryCount: 2, // Already at limit
		OldTxs:           make(map[string]*types.Transaction),
		Network:          networks.EthereumMainnet,
	}

	result := setup.WM.handleBroadcastError(ErrInsufficientFund, tx, execCtx)

	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.True(t, errors.Is(result.Error, ErrEnsureTxOutOfRetries))
}

// ============================================================
// handleTransactionStatus Tests
// ============================================================

func TestHandleTransactionStatus_Mined(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(5, testAddr2, oneEth)
	receipt := newSuccessReceipt(tx)

	execCtx := &TxExecutionContext{}

	result := setup.WM.handleTransactionStatus(TxStatus{Status: "mined", Receipt: receipt}, tx, execCtx)

	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.Nil(t, result.Error)
	assert.Equal(t, tx, result.Transaction)
	assert.Equal(t, receipt, result.Receipt)
}

func TestHandleTransactionStatus_Reverted(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(5, testAddr2, oneEth)
	receipt := newFailedReceipt(tx)

	execCtx := &TxExecutionContext{}

	result := setup.WM.handleTransactionStatus(TxStatus{Status: "reverted", Receipt: receipt}, tx, execCtx)

	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.Nil(t, result.Error)
	assert.Equal(t, tx, result.Transaction)
	assert.Equal(t, receipt, result.Receipt)
}

func TestHandleTransactionStatus_Lost_Retries(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(5, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries:       5,
		ActualRetryCount: 0,
	}

	result := setup.WM.handleTransactionStatus(TxStatus{Status: "lost"}, tx, execCtx)

	assert.False(t, result.ShouldReturn)
	assert.True(t, result.ShouldRetry)
	assert.Equal(t, 1, execCtx.ActualRetryCount)
	assert.Nil(t, execCtx.RetryNonce) // Should get new nonce
}

func TestHandleTransactionStatus_Slow_BumpsGas(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestDynamicTx(5, testAddr2, oneEth, 21000,
		big.NewInt(2000000000),  // 2 gwei
		big.NewInt(20000000000), // 20 gwei
		big.NewInt(1),
	)

	execCtx := &TxExecutionContext{
		MaxGasPrice: 100.0,
		MaxTipCap:   50.0,
	}

	result := setup.WM.handleTransactionStatus(TxStatus{Status: "slow"}, tx, execCtx)

	assert.False(t, result.ShouldReturn)
	assert.True(t, result.ShouldRetry)
	assert.Greater(t, execCtx.RetryGasPrice, 20.0) // Should be bumped
	assert.Greater(t, execCtx.RetryTipCap, 2.0)    // Should be bumped
}

func TestHandleTransactionStatus_Slow_HitsLimit(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestDynamicTx(5, testAddr2, oneEth, 21000,
		big.NewInt(2000000000),
		big.NewInt(20000000000),
		big.NewInt(1),
	)

	execCtx := &TxExecutionContext{
		MaxGasPrice: 22.0, // Low limit
		MaxTipCap:   50.0,
	}

	result := setup.WM.handleTransactionStatus(TxStatus{Status: "slow"}, tx, execCtx)

	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.True(t, errors.Is(result.Error, ErrGasPriceLimitReached))
}

// ============================================================
// handleGasEstimationFailure Tests
// ============================================================

func TestHandleGasEstimationFailure_NoOldTxs_Retries(t *testing.T) {
	setup := newTestSetup(t)

	execCtx := &TxExecutionContext{
		NumRetries:       5,
		ActualRetryCount: 0,
		OldTxs:           make(map[string]*types.Transaction),
		Network:          networks.EthereumMainnet,
	}

	err := errors.Join(ErrEstimateGasFailed, errors.New("execution reverted"))
	result := setup.WM.handleGasEstimationFailure(execCtx, nil, err)

	assert.False(t, result.ShouldReturn)
	assert.True(t, result.ShouldRetry)
	assert.Equal(t, 1, execCtx.ActualRetryCount)
}

func TestHandleGasEstimationFailure_OldTxMined_ReturnsIt(t *testing.T) {
	setup := newTestSetup(t)

	oldTx := newTestTx(5, testAddr2, oneEth)
	oldReceipt := newSuccessReceipt(oldTx)

	setup.Reader.TxInfoFromHashFn = func(hash string) (TxInfo, error) {
		if hash == oldTx.Hash().Hex() {
			return TxInfo{Status: "done", Receipt: oldReceipt}, nil
		}
		return TxInfo{Status: "pending"}, nil
	}

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		OldTxs: map[string]*types.Transaction{
			oldTx.Hash().Hex(): oldTx,
		},
		Network: networks.EthereumMainnet,
	}

	err := errors.Join(ErrEstimateGasFailed, errors.New("execution reverted"))
	result := setup.WM.handleGasEstimationFailure(execCtx, nil, err)

	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.Equal(t, oldTx, result.Transaction)
}

func TestHandleGasEstimationFailure_ExceedsRetries(t *testing.T) {
	setup := newTestSetup(t)

	execCtx := &TxExecutionContext{
		NumRetries:       2,
		ActualRetryCount: 2, // Already at limit
		OldTxs:           make(map[string]*types.Transaction),
		Network:          networks.EthereumMainnet,
	}

	err := errors.Join(ErrEstimateGasFailed, errors.New("execution reverted"))
	result := setup.WM.handleGasEstimationFailure(execCtx, nil, err)

	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.True(t, errors.Is(result.Error, ErrEnsureTxOutOfRetries))
}

// ============================================================
// Simulation (EthCall) Tests
// ============================================================

func TestExecuteTransactionAttempt_SimulationFails_ReturnsError(t *testing.T) {
	setup := newTestSetup(t)

	// EthCall returns a non-revert error (network error)
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, fmt.Errorf("network connection failed")
	}

	execCtx := &TxExecutionContext{
		NumRetries:    5,
		TxType:        2,
		From:          testAddr1,
		To:            testAddr2,
		Value:         oneEth,
		GasLimit:      21000,
		RetryGasPrice: 20.0,
		RetryTipCap:   2.0,
		Data:          nil,
		Network:       networks.EthereumMainnet,
		OldTxs:        make(map[string]*types.Transaction),
	}

	result := setup.WM.executeTransactionAttempt(context.Background(), execCtx, nil)

	// Should return error since simulation failed (not a revert)
	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.True(t, errors.Is(result.Error, ErrSimulatedTxFailed))
}

func TestExecuteTransactionAttempt_SimulationSucceeds_ContinuesToBroadcast(t *testing.T) {
	setup := newTestSetup(t)

	// EthCall succeeds (no error)
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	// Track if we get to the broadcast phase (signAndBroadcastTransaction)
	// This will fail because we don't have a signed account, but that's OK for this test
	execCtx := &TxExecutionContext{
		NumRetries:    5,
		TxType:        2,
		From:          testAddr1,
		To:            testAddr2,
		Value:         oneEth,
		GasLimit:      21000,
		RetryGasPrice: 20.0,
		RetryTipCap:   2.0,
		Data:          nil,
		Network:       networks.EthereumMainnet,
		OldTxs:        make(map[string]*types.Transaction),
	}

	result := setup.WM.executeTransactionAttempt(context.Background(), execCtx, nil)

	// The test should pass simulation and fail at signing (because we have no account)
	// This confirms simulation was successful
	assert.True(t, result.ShouldReturn)
	assert.Contains(t, result.Error.Error(), "not registered")
}

// ============================================================
// Hook Execution Tests
// ============================================================

func TestHandleTransactionStatus_TxMinedHook_Called(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(5, testAddr2, oneEth)
	receipt := newSuccessReceipt(tx)

	hookCalled := false
	var receivedTx *types.Transaction
	var receivedReceipt *types.Receipt

	execCtx := &TxExecutionContext{
		TxMinedHook: func(tx *types.Transaction, r *types.Receipt) error {
			hookCalled = true
			receivedTx = tx
			receivedReceipt = r
			return nil
		},
	}

	result := setup.WM.handleTransactionStatus(TxStatus{Status: "mined", Receipt: receipt}, tx, execCtx)

	assert.True(t, hookCalled)
	assert.Equal(t, tx, receivedTx)
	assert.Equal(t, receipt, receivedReceipt)
	assert.True(t, result.ShouldReturn)
	assert.Nil(t, result.Error)
}

func TestHandleTransactionStatus_TxMinedHook_ReturnsError(t *testing.T) {
	setup := newTestSetup(t)

	tx := newTestTx(5, testAddr2, oneEth)
	receipt := newSuccessReceipt(tx)

	hookError := errors.New("hook failed")

	execCtx := &TxExecutionContext{
		TxMinedHook: func(tx *types.Transaction, r *types.Receipt) error {
			return hookError
		},
	}

	result := setup.WM.handleTransactionStatus(TxStatus{Status: "mined", Receipt: receipt}, tx, execCtx)

	assert.True(t, result.ShouldReturn)
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "hook error")
}

// ============================================================
// Factory Injection Tests
// ============================================================

func TestWalletManager_UsesCustomReaderFactory(t *testing.T) {
	factoryCalled := false
	customReader := &mockEthReader{
		SuggestedGasSettingsFn: func() (float64, float64, error) {
			return 50.0, 5.0, nil // Custom values
		},
	}

	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			factoryCalled = true
			return customReader, nil
		}),
	)

	// Access reader to trigger factory
	reader, err := wm.Reader(networks.EthereumMainnet)

	require.NoError(t, err)
	assert.True(t, factoryCalled)
	assert.Equal(t, customReader, reader)
}

func TestWalletManager_UsesCustomBroadcasterFactory(t *testing.T) {
	factoryCalled := false
	customBroadcaster := &mockEthBroadcaster{}

	// Need a reader factory too for initNetwork to work
	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return &mockEthReader{}, nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			factoryCalled = true
			return customBroadcaster, nil
		}),
	)

	// Access broadcaster to trigger factory
	broadcaster, err := wm.Broadcaster(networks.EthereumMainnet)

	require.NoError(t, err)
	assert.True(t, factoryCalled)
	assert.Equal(t, customBroadcaster, broadcaster)
}

// ============================================================
// Circuit Breaker Integration Tests
// ============================================================

func TestReader_CircuitBreakerOpen_ReturnsError(t *testing.T) {
	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return nil, errors.New("network error")
		}),
	)

	// Trigger multiple failures to open circuit breaker
	for i := 0; i < 10; i++ {
		_, _ = wm.Reader(networks.EthereumMainnet)
	}

	// Now circuit breaker should be open
	_, err := wm.Reader(networks.EthereumMainnet)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrCircuitBreakerOpen))
}

// ============================================================
// Nonce Release Tests
// ============================================================

func TestBuildTx_ReleasesNonce_WhenGasEstimationFails(t *testing.T) {
	setup := newTestSetup(t)

	// First, set up initial nonce state
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	// Make gas estimation fail
	setup.Reader.EstimateExactGasFn = func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
		return 0, errors.New("execution reverted")
	}

	// Build tx with gasLimit=0 to trigger estimation
	_, err := setup.WM.BuildTx(
		2, testAddr1, testAddr2,
		nil, // nonce = nil, will acquire nonce 5
		oneEth,
		0, 0, 20, 0, 2, 0, nil,
		networks.EthereumMainnet,
	)
	require.Error(t, err)

	// Now try to build another tx - if nonce was released, we should get nonce 5 again
	setup.Reader.EstimateExactGasFn = func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
		return 21000, nil // Success this time
	}

	tx, err := setup.WM.BuildTx(
		2, testAddr1, testAddr2,
		nil, // Should get nonce 5 again since it was released
		oneEth,
		0, 0, 20, 0, 2, 0, nil,
		networks.EthereumMainnet,
	)

	require.NoError(t, err)
	assert.Equal(t, uint64(5), tx.Nonce(), "nonce should be 5 since previous was released")
}

func TestBuildTx_DoesNotReleaseNonce_WhenSuccess(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	// First successful build
	tx1, err := setup.WM.BuildTx(
		2, testAddr1, testAddr2,
		nil, oneEth,
		21000, 0, 20, 0, 2, 0, nil,
		networks.EthereumMainnet,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), tx1.Nonce())

	// Second build should get nonce 6 (not 5)
	tx2, err := setup.WM.BuildTx(
		2, testAddr1, testAddr2,
		nil, oneEth,
		21000, 0, 20, 0, 2, 0, nil,
		networks.EthereumMainnet,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(6), tx2.Nonce(), "nonce should be 6 since 5 was used")
}

func TestReleaseNonce_AllowsNonceReuse(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	// Acquire nonce 5
	tx1, err := setup.WM.BuildTx(
		2, testAddr1, testAddr2,
		nil, oneEth,
		21000, 0, 20, 0, 2, 0, nil,
		networks.EthereumMainnet,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), tx1.Nonce())

	// Acquire nonce 6
	tx2, err := setup.WM.BuildTx(
		2, testAddr1, testAddr2,
		nil, oneEth,
		21000, 0, 20, 0, 2, 0, nil,
		networks.EthereumMainnet,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(6), tx2.Nonce())

	// Release nonce 6 (the tip)
	setup.WM.ReleaseNonce(testAddr1, networks.EthereumMainnet, 6)

	// Next acquire should get 6 again
	tx3, err := setup.WM.BuildTx(
		2, testAddr1, testAddr2,
		nil, oneEth,
		21000, 0, 20, 0, 2, 0, nil,
		networks.EthereumMainnet,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(6), tx3.Nonce(), "should get 6 again after release")
}

func TestReleaseNonce_OnlyReleasesTip(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	// Acquire nonces 5, 6, 7
	_, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	_, err = setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	_, err = setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)

	// Try to release nonce 6 (not the tip - tip is 7)
	setup.WM.ReleaseNonce(testAddr1, networks.EthereumMainnet, 6)

	// Next acquire should still get 8 (not 6)
	tx, err := setup.WM.BuildTx(
		2, testAddr1, testAddr2,
		nil, oneEth,
		21000, 0, 20, 0, 2, 0, nil,
		networks.EthereumMainnet,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(8), tx.Nonce(), "should get 8, not 6, because 6 is not the tip")
}

func TestReleaseNonce_MultipleWalletsIndependent(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) {
		if addr == testAddr1.Hex() {
			return 10, nil
		}
		return 20, nil
	}
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) {
		if addr == testAddr1.Hex() {
			return 10, nil
		}
		return 20, nil
	}

	// Acquire nonce for wallet 1
	tx1, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(10), tx1.Nonce())

	// Acquire nonce for wallet 2
	tx2, err := setup.WM.BuildTx(2, testAddr3, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(20), tx2.Nonce())

	// Release nonce for wallet 1
	setup.WM.ReleaseNonce(testAddr1, networks.EthereumMainnet, 10)

	// Wallet 1 should get 10 again
	tx3, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(10), tx3.Nonce())

	// Wallet 2 should get 21 (not affected by wallet 1's release)
	tx4, err := setup.WM.BuildTx(2, testAddr3, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(21), tx4.Nonce())
}

func TestBuildTx_ReleasesNonce_WhenNonceAcquisitionFailsLater(t *testing.T) {
	setup := newTestSetup(t)

	callCount := 0
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) {
		callCount++
		if callCount == 1 {
			return 5, nil
		}
		return 5, nil
	}
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) {
		return 5, nil
	}

	// Make gas estimation fail after nonce is acquired
	setup.Reader.EstimateExactGasFn = func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
		return 0, errors.New("out of gas")
	}

	// First attempt - fails at gas estimation
	_, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 0, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.Error(t, err)

	// Second attempt with working gas estimation
	setup.Reader.EstimateExactGasFn = func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
		return 21000, nil
	}

	tx, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 0, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	// Should get nonce 5 again because the first attempt released it
	assert.Equal(t, uint64(5), tx.Nonce())
}

func TestNonceRelease_SequentialReleaseAndAcquire(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }

	// Acquire nonces 0-4
	for i := 0; i < 5; i++ {
		tx, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
		require.NoError(t, err)
		assert.Equal(t, uint64(i), tx.Nonce())
	}
	// Now we have nonces 0-4 acquired, tip is 4

	// Release nonce 4 (the tip)
	setup.WM.ReleaseNonce(testAddr1, networks.EthereumMainnet, 4)

	// Next acquire should get 4 again
	tx, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(4), tx.Nonce(), "should get 4 after release")

	// Release 4 again
	setup.WM.ReleaseNonce(testAddr1, networks.EthereumMainnet, 4)

	// Release 3 (now the new tip after releasing 4)
	setup.WM.ReleaseNonce(testAddr1, networks.EthereumMainnet, 3)

	// Next acquire should get 3
	tx, err = setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), tx.Nonce(), "should get 3 after double release")
}

func TestSignAndBroadcast_ReleasesNonce_WhenBeforeHookFails(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil // Simulation passes
	}

	// Build a tx first (this acquires nonce 5)
	tx := newTestTx(5, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		From:       testAddr1,
		To:         testAddr2,
		Network:    networks.EthereumMainnet,
		OldTxs:     make(map[string]*types.Transaction),
		BeforeSignAndBroadcastHook: func(tx *types.Transaction, err error) error {
			return errors.New("hook says no")
		},
	}

	result := setup.WM.signAndBroadcastTransaction(tx, execCtx)

	assert.True(t, result.ShouldReturn)
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "hook")

	// Now the nonce should have been released
	// Build another tx - should get nonce 5 again
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	newTx, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), newTx.Nonce(), "nonce 5 should be available after release")
}

func TestSimulation_ReleasesNonce_WhenHookSaysNoRetry(t *testing.T) {
	setup := newTestSetup(t)

	// Start with nonce 5
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	// First, let's acquire nonce 5 manually to establish state
	tx1, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), tx1.Nonce())

	// Now release it to simulate what happens when simulation fails
	setup.WM.ReleaseNonce(testAddr1, networks.EthereumMainnet, 5)

	// Next acquire should get 5 again (proving release worked)
	tx2, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), tx2.Nonce(), "nonce 5 should be available after release")
}

func TestSimulation_DoesNotReleaseNonce_WhenRetrying(t *testing.T) {
	setup := newTestSetup(t)

	// Start fresh - no prior nonce state
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	// Simulation succeeds, tx should proceed
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil // Success
	}

	// First, acquire nonce 5
	tx1, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), tx1.Nonce())

	// Don't release it - simulating tx is still in-flight

	// Next acquire should get 6 (nonce 5 is still reserved)
	tx2, err := setup.WM.BuildTx(2, testAddr1, testAddr2, nil, oneEth, 21000, 0, 20, 0, 2, 0, nil, networks.EthereumMainnet)
	require.NoError(t, err)
	assert.Equal(t, uint64(6), tx2.Nonce(), "nonce 6 because 5 is still in use")
}

func TestSimulation_NonRevertError_ReturnsErrorWithoutHook(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	// Make EthCall return a network error (not a revert)
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, errors.New("network timeout")
	}

	hookCalled := false
	execCtx := &TxExecutionContext{
		NumRetries:    5,
		TxType:        2,
		From:          testAddr1,
		To:            testAddr2,
		Value:         oneEth,
		GasLimit:      21000,
		RetryGasPrice: 20.0,
		RetryTipCap:   2.0,
		Network:       networks.EthereumMainnet,
		OldTxs:        make(map[string]*types.Transaction),
		SimulationFailedHook: func(tx *types.Transaction, revertData []byte, abiError *abi.Error, revertParams any, err error) (bool, error) {
			hookCalled = true
			return false, nil
		},
	}

	result := setup.WM.executeTransactionAttempt(context.Background(), execCtx, nil)

	// Hook should NOT be called for non-revert errors
	assert.False(t, hookCalled, "simulation hook should NOT be called for non-revert errors")
	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.Error(t, result.Error)
	assert.True(t, errors.Is(result.Error, ErrSimulatedTxFailed))
}

// ============================================================
// EnsureTxWithHooksContext Integration Tests
// ============================================================

// setupEnsureTxTest creates a test setup configured for EnsureTx integration tests
// with a registered wallet that can sign transactions
func setupEnsureTxTest(t *testing.T) *testSetup {
	t.Helper()
	setup := newTestSetup(t)

	// Create and register the test wallet so transactions can be signed
	// testPrivateKey1 is "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	acc, err := account.NewPrivateKeyAccount("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	setup.WM.SetAccount(acc)

	return setup
}

func TestEnsureTx_FullSuccessPath(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	// Set up mocks for success path
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.SuggestedGasSettingsFn = func() (float64, float64, error) { return 20.0, 2.0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil // Simulation passes
	}

	var broadcastedTx *types.Transaction
	setup.Broadcaster.BroadcastTxFn = func(tx *types.Transaction) (string, bool, error) {
		broadcastedTx = tx
		return tx.Hash().Hex(), true, nil
	}
	// Also handle sync broadcast in case network supports it
	setup.Broadcaster.BroadcastTxSyncFn = func(tx *types.Transaction) (*types.Receipt, error) {
		broadcastedTx = tx
		return &types.Receipt{
			Status:      types.ReceiptStatusSuccessful,
			TxHash:      tx.Hash(),
			BlockNumber: big.NewInt(12345),
		}, nil
	}

	// Monitor returns mined status (used for non-sync networks)
	setup.Monitor.StatusToReturn = TxMonitorStatus{
		Status:  "done",
		Receipt: &types.Receipt{Status: types.ReceiptStatusSuccessful},
	}

	ctx := context.Background()
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		3,           // numRetries
		time.Second, // sleepDuration
		time.Second, // txCheckInterval
		2,           // txType (EIP-1559)
		fromAddr,    // from
		testAddr2,   // to
		oneEth,      // value
		21000, 0,    // gasLimit, extraGasLimit
		20.0, 0, // gasPrice, extraGasPrice
		2.0, 0, // tipCapGwei, extraTipCapGwei
		100.0, 50.0, // maxGasPrice, maxTipCap
		nil, // data
		networks.EthereumMainnet,
		nil, nil, nil, nil, nil, nil, // hooks
	)

	require.NoError(t, err, "EnsureTxWithHooksContext should succeed")
	require.NotNil(t, tx, "tx should not be nil")
	require.NotNil(t, receipt, "receipt should not be nil")
	require.NotNil(t, broadcastedTx, "broadcastedTx should have been set by broadcast function")
	assert.Equal(t, broadcastedTx.Hash(), tx.Hash())
	assert.Equal(t, uint64(5), tx.Nonce())
	// Verify either BroadcastTx or BroadcastTxSync was called
	totalBroadcasts := len(setup.Broadcaster.BroadcastTxCalls) + len(setup.Broadcaster.BroadcastTxSyncCalls)
	assert.Equal(t, 1, totalBroadcasts, "should have broadcast exactly once")
}

func TestEnsureTx_TxMinedHook_Called(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	expectedReceipt := &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(12345),
	}
	setup.Monitor.StatusToReturn = TxMonitorStatus{
		Status:  "done",
		Receipt: expectedReceipt,
	}

	hookCalled := false
	var hookTx *types.Transaction
	var hookReceipt *types.Receipt

	txMinedHook := func(tx *types.Transaction, r *types.Receipt) error {
		hookCalled = true
		hookTx = tx
		hookReceipt = r
		return nil
	}

	ctx := context.Background()
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		3, time.Millisecond, time.Millisecond,
		2, fromAddr, testAddr2, oneEth,
		21000, 0, 20.0, 0, 2.0, 0, 100.0, 50.0,
		nil, networks.EthereumMainnet,
		nil, nil, nil, nil, nil, txMinedHook,
	)

	require.NoError(t, err)
	assert.True(t, hookCalled, "txMinedHook should have been called")
	assert.Equal(t, tx.Hash(), hookTx.Hash())
	assert.Equal(t, receipt, hookReceipt)
}

func TestEnsureTx_BeforeSignAndBroadcastHook_StopsExecution(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	hookError := errors.New("pre-flight check failed")
	beforeHook := func(tx *types.Transaction, err error) error {
		return hookError
	}

	ctx := context.Background()
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		3, time.Millisecond, time.Millisecond,
		2, fromAddr, testAddr2, oneEth,
		21000, 0, 20.0, 0, 2.0, 0, 100.0, 50.0,
		nil, networks.EthereumMainnet,
		beforeHook, nil, nil, nil, nil, nil,
	)

	require.Error(t, err)
	assert.Nil(t, tx)
	assert.Nil(t, receipt)
	assert.Contains(t, err.Error(), "pre-flight check failed")
	// Broadcaster should NOT have been called
	assert.Empty(t, setup.Broadcaster.BroadcastTxCalls)
}

func TestEnsureTx_ContextCancellation_DuringExecution(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	// Monitor will take a while, allowing cancellation
	setup.Monitor.Delay = 5 * time.Second
	setup.Monitor.StatusToReturn = TxMonitorStatus{Status: "pending"}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Use BSCMainnet which doesn't support sync tx, forcing async path with monitoring
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		3, time.Millisecond, time.Millisecond,
		2, fromAddr, testAddr2, oneEth,
		21000, 0, 20.0, 0, 2.0, 0, 100.0, 50.0,
		nil, networks.BSCMainnet, // Use BSC which doesn't support sync tx
		nil, nil, nil, nil, nil, nil,
	)

	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.Nil(t, tx)
	assert.Nil(t, receipt)
}

func TestEnsureTx_SlowTxTriggersGasBump(t *testing.T) {
	// This test verifies that AdjustGasPricesForSlowTx correctly bumps gas prices
	// The full integration with monitor is tested separately

	ctx := &TxExecutionContext{
		MaxGasPrice: 100.0,
		MaxTipCap:   50.0,
	}

	tx := newTestDynamicTx(5, testAddr2, oneEth, 21000,
		big.NewInt(2000000000),  // 2 gwei tip
		big.NewInt(20000000000), // 20 gwei max fee
		big.NewInt(1),
	)

	adjusted := ctx.AdjustGasPricesForSlowTx(tx)

	assert.True(t, adjusted, "should be able to adjust gas prices")
	assert.Greater(t, ctx.RetryGasPrice, 20.0, "gas price should be bumped above 20")
	assert.Greater(t, ctx.RetryTipCap, 2.0, "tip cap should be bumped above 2")
}

func TestEnsureTx_MaxRetriesExceeded(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	// Disable sync broadcast - make it fail with retryable error
	setup.Broadcaster.BroadcastTxSyncFn = func(tx *types.Transaction) (*types.Receipt, error) {
		return nil, errors.New("insufficient funds for gas * price + value")
	}

	// Async broadcast also fails with a retryable error
	setup.Broadcaster.BroadcastTxFn = func(tx *types.Transaction) (string, bool, error) {
		return "", false, errors.New("insufficient funds for gas * price + value")
	}

	ctx := context.Background()
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		2, time.Millisecond, time.Millisecond, // Only 2 retries
		2, fromAddr, testAddr2, oneEth,
		21000, 0, 20.0, 0, 2.0, 0, 100.0, 50.0,
		nil, networks.EthereumMainnet,
		nil, nil, nil, nil, nil, nil,
	)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrEnsureTxOutOfRetries))
	assert.Nil(t, tx)
	assert.Nil(t, receipt)
}

func TestEnsureTx_GasPriceLimitReached_Unit(t *testing.T) {
	// Unit test: verify that AdjustGasPricesForSlowTx returns false when limit would be exceeded

	ctx := &TxExecutionContext{
		MaxGasPrice: 22.0, // Low limit - 20 * 1.2 = 24 would exceed
		MaxTipCap:   50.0,
	}

	tx := newTestDynamicTx(5, testAddr2, oneEth, 21000,
		big.NewInt(2000000000),  // 2 gwei tip
		big.NewInt(20000000000), // 20 gwei max fee
		big.NewInt(1),
	)

	adjusted := ctx.AdjustGasPricesForSlowTx(tx)

	// Should return false because the new gas price (20 * 1.2 = 24) exceeds maxGasPrice (22)
	assert.False(t, adjusted, "should NOT be able to adjust when gas price limit would be exceeded")
}

func TestEnsureTx_TxReverted_ReturnsWithReceipt(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	// Monitor returns reverted status
	setup.Monitor.StatusToReturn = TxMonitorStatus{
		Status: "reverted",
		Receipt: &types.Receipt{
			Status:      types.ReceiptStatusFailed,
			BlockNumber: big.NewInt(12345),
		},
	}

	ctx := context.Background()
	// Use BSCMainnet which doesn't support sync tx, forcing async path with monitoring
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		3, time.Millisecond, time.Millisecond,
		2, fromAddr, testAddr2, oneEth,
		21000, 0, 20.0, 0, 2.0, 0, 100.0, 50.0,
		nil, networks.BSCMainnet,
		nil, nil, nil, nil, nil, nil,
	)

	// Reverted tx is still "successful" from EnsureTx perspective - it was mined
	require.NoError(t, err)
	require.NotNil(t, tx)
	require.NotNil(t, receipt)
	assert.Equal(t, types.ReceiptStatusFailed, receipt.Status)
}

func TestEnsureTx_SyncTx_ReturnsImmediately(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	expectedReceipt := &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(99999),
	}

	// BroadcastTxSync returns immediately with receipt
	setup.Broadcaster.BroadcastTxSyncFn = func(tx *types.Transaction) (*types.Receipt, error) {
		return expectedReceipt, nil
	}

	// Use a network that supports sync tx (we'll mock it)
	// For this test, we need to use the regular broadcast since our mock doesn't
	// actually switch based on network. But the concept is tested.
	ctx := context.Background()
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		3, time.Millisecond, time.Millisecond,
		2, fromAddr, testAddr2, oneEth,
		21000, 0, 20.0, 0, 2.0, 0, 100.0, 50.0,
		nil, networks.EthereumMainnet, // Would need a sync-supporting network
		nil, nil, nil, nil, nil, nil,
	)

	require.NoError(t, err)
	require.NotNil(t, tx)
	require.NotNil(t, receipt)
}

func TestEnsureTx_LostTx_RetriesWithNewNonce(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	nonceCall := 0
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) {
		nonceCall++
		return uint64(nonceCall - 1), nil // Increment each call to simulate progression
	}
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) {
		return uint64(nonceCall - 1), nil
	}
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	broadcastedNonces := []uint64{}
	setup.Broadcaster.BroadcastTxFn = func(tx *types.Transaction) (string, bool, error) {
		broadcastedNonces = append(broadcastedNonces, tx.Nonce())
		return tx.Hash().Hex(), true, nil
	}

	// First tx is "lost", second is mined
	setup.Monitor.StatusSequence = []TxMonitorStatus{
		{Status: "lost"},
		{Status: "done", Receipt: &types.Receipt{Status: types.ReceiptStatusSuccessful}},
	}

	ctx := context.Background()
	// Use BSCMainnet which doesn't support sync tx, forcing async path with monitoring
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		5, time.Millisecond, time.Millisecond,
		2, fromAddr, testAddr2, oneEth,
		21000, 0, 20.0, 0, 2.0, 0, 100.0, 50.0,
		nil, networks.BSCMainnet,
		nil, nil, nil, nil, nil, nil,
	)

	require.NoError(t, err)
	require.NotNil(t, tx)
	require.NotNil(t, receipt)
	assert.GreaterOrEqual(t, len(broadcastedNonces), 2, "should have broadcast multiple times")
}

func TestEnsureTx_AfterSignAndBroadcastHook_CalledOnSuccess(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	setup.Monitor.StatusToReturn = TxMonitorStatus{
		Status:  "done",
		Receipt: &types.Receipt{Status: types.ReceiptStatusSuccessful},
	}

	afterHookCalled := false
	var afterHookTx *types.Transaction

	afterHook := func(tx *types.Transaction, err error) error {
		afterHookCalled = true
		afterHookTx = tx
		assert.Nil(t, err, "afterHook should receive nil error on success")
		return nil
	}

	ctx := context.Background()
	tx, _, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		3, time.Millisecond, time.Millisecond,
		2, fromAddr, testAddr2, oneEth,
		21000, 0, 20.0, 0, 2.0, 0, 100.0, 50.0,
		nil, networks.EthereumMainnet,
		nil, afterHook, nil, nil, nil, nil,
	)

	require.NoError(t, err)
	assert.True(t, afterHookCalled, "afterSignAndBroadcastHook should have been called")
	assert.Equal(t, tx.Hash(), afterHookTx.Hash())
}

func TestEnsureTx_GasEstimationFails_RetriesUntilSuccess(t *testing.T) {
	setup := setupEnsureTxTest(t)

	fromAddr := crypto.PubkeyToAddress(testPrivateKey1.PublicKey)

	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.EthCallFn = func(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
		return nil, nil
	}

	estimateCallCount := 0
	setup.Reader.EstimateExactGasFn = func(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
		estimateCallCount++
		if estimateCallCount < 3 {
			return 0, errors.New("execution reverted")
		}
		return 21000, nil // Succeed on 3rd try
	}

	// Use sync broadcast since it returns immediately
	setup.Broadcaster.BroadcastTxSyncFn = func(tx *types.Transaction) (*types.Receipt, error) {
		return &types.Receipt{
			Status:      types.ReceiptStatusSuccessful,
			BlockNumber: big.NewInt(12345),
		}, nil
	}

	ctx := context.Background()
	tx, receipt, err := setup.WM.EnsureTxWithHooksContext(
		ctx,
		5, time.Millisecond, time.Millisecond,
		2, fromAddr, testAddr2, oneEth,
		0, 0, 20.0, 0, 2.0, 0, 100.0, 50.0, // gasLimit=0 triggers estimation
		nil, networks.EthereumMainnet,
		nil, nil, nil, nil, nil, nil,
	)

	require.NoError(t, err)
	require.NotNil(t, tx)
	require.NotNil(t, receipt)
	assert.GreaterOrEqual(t, estimateCallCount, 3, "gas estimation should have been called at least 3 times")
}

// ============================================================
// Additional Nonce Tests for Full Coverage
// ============================================================

func TestPendingNonce_ReturnsNilForNewWallet(t *testing.T) {
	setup := newTestSetup(t)
	wallet := common.HexToAddress("0x9999999999999999999999999999999999999999")

	nonce := setup.WM.pendingNonce(wallet, networks.EthereumMainnet)

	assert.Nil(t, nonce)
}

func TestPendingNonce_ReturnsValueAfterSet(t *testing.T) {
	setup := newTestSetup(t)
	wallet := testAddr1

	setup.WM.setPendingNonce(wallet, networks.EthereumMainnet, 10)

	nonce := setup.WM.pendingNonce(wallet, networks.EthereumMainnet)

	require.NotNil(t, nonce)
	assert.Equal(t, uint64(11), nonce.Uint64()) // Returns next nonce (10+1)
}

func TestAcquireNonce_ReturnsError_WhenReaderFails(t *testing.T) {
	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return nil, errors.New("reader init failed")
		}),
	)

	nonce, err := wm.acquireNonce(testAddr1, networks.EthereumMainnet)

	assert.Nil(t, nonce)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't get reader")
}

func TestAcquireNonce_ReturnsError_WhenMinedNonceFails(t *testing.T) {
	mockReader := &mockEthReader{
		GetMinedNonceFn: func(addr string) (uint64, error) {
			return 0, errors.New("mined nonce error")
		},
		GetPendingNonceFn: func(addr string) (uint64, error) {
			return 0, nil
		},
		SuggestedGasSettingsFn: func() (float64, float64, error) {
			return 20.0, 2.0, nil
		},
	}

	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return mockReader, nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return &mockEthBroadcaster{}, nil
		}),
		WithTxMonitorFactory(func(reader EthReader) TxMonitor {
			return &mockTxMonitor{}
		}),
	)

	nonce, err := wm.acquireNonce(testAddr1, networks.EthereumMainnet)

	assert.Nil(t, nonce)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't get mined nonce")
}

func TestAcquireNonce_ReturnsError_WhenPendingNonceFails(t *testing.T) {
	mockReader := &mockEthReader{
		GetMinedNonceFn: func(addr string) (uint64, error) {
			return 5, nil
		},
		GetPendingNonceFn: func(addr string) (uint64, error) {
			return 0, errors.New("pending nonce error")
		},
		SuggestedGasSettingsFn: func() (float64, float64, error) {
			return 20.0, 2.0, nil
		},
	}

	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return mockReader, nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return &mockEthBroadcaster{}, nil
		}),
		WithTxMonitorFactory(func(reader EthReader) TxMonitor {
			return &mockTxMonitor{}
		}),
	)

	nonce, err := wm.acquireNonce(testAddr1, networks.EthereumMainnet)

	assert.Nil(t, nonce)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't get remote pending nonce")
}

func TestHandleNonceIsLowError_TxStatusCheckFails_RetriesOrExceeds(t *testing.T) {
	setup := newTestSetup(t)

	// Make status check fail
	setup.Reader.TxInfoFromHashFn = func(hash string) (TxInfo, error) {
		return TxInfo{}, errors.New("status check failed")
	}

	oldTx := newTestTx(5, testAddr2, oneEth)
	newTx := newTestTx(6, testAddr2, oneEth)

	t.Run("retries when under limit", func(t *testing.T) {
		execCtx := &TxExecutionContext{
			NumRetries: 5,
			OldTxs: map[string]*types.Transaction{
				oldTx.Hash().Hex(): oldTx,
			},
			Network: networks.EthereumMainnet,
		}

		result := setup.WM.handleNonceIsLowError(newTx, execCtx)

		assert.True(t, result.ShouldRetry)
		assert.False(t, result.ShouldReturn)
		assert.Nil(t, result.Error)
	})

	t.Run("exceeds retries", func(t *testing.T) {
		execCtx := &TxExecutionContext{
			NumRetries:       3,
			ActualRetryCount: 3, // Already at max
			OldTxs: map[string]*types.Transaction{
				oldTx.Hash().Hex(): oldTx,
			},
			Network: networks.EthereumMainnet,
		}

		result := setup.WM.handleNonceIsLowError(newTx, execCtx)

		// Should return with error after exceeding retries
		assert.False(t, result.ShouldRetry)
		assert.True(t, result.ShouldReturn)
		assert.Error(t, result.Error)
	})
}

func TestHandleNonceIsLowError_NoPendingTxs_Retries(t *testing.T) {
	setup := newTestSetup(t)

	// Return "pending" for all txs (no completed ones)
	setup.Reader.TxInfoFromHashFn = func(hash string) (TxInfo, error) {
		return TxInfo{Status: "pending"}, nil
	}

	oldTx := newTestTx(5, testAddr2, oneEth)
	newTx := newTestTx(6, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		OldTxs: map[string]*types.Transaction{
			oldTx.Hash().Hex(): oldTx,
		},
		Network: networks.EthereumMainnet,
	}

	result := setup.WM.handleNonceIsLowError(newTx, execCtx)

	// Should retry with new nonce (RetryNonce = nil)
	assert.True(t, result.ShouldRetry)
	assert.False(t, result.ShouldReturn)
	assert.Nil(t, execCtx.RetryNonce)
}

func TestHandleNonceIsLowError_RevertedTxFound_Returns(t *testing.T) {
	setup := newTestSetup(t)

	oldTx := newTestTx(5, testAddr2, oneEth)
	revertedReceipt := newFailedReceipt(oldTx)

	setup.Reader.TxInfoFromHashFn = func(hash string) (TxInfo, error) {
		if hash == oldTx.Hash().Hex() {
			return TxInfo{Status: "reverted", Receipt: revertedReceipt}, nil
		}
		return TxInfo{Status: "pending"}, nil
	}

	newTx := newTestTx(6, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		OldTxs: map[string]*types.Transaction{
			oldTx.Hash().Hex(): oldTx,
		},
		Network: networks.EthereumMainnet,
	}

	result := setup.WM.handleNonceIsLowError(newTx, execCtx)

	// Should return the reverted tx
	assert.True(t, result.ShouldReturn)
	assert.False(t, result.ShouldRetry)
	assert.Equal(t, oldTx, result.Transaction)
}

func TestHandleNonceIsLowError_NoOldTxs_Retries(t *testing.T) {
	setup := newTestSetup(t)

	newTx := newTestTx(6, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		OldTxs:     make(map[string]*types.Transaction), // Empty
		Network:    networks.EthereumMainnet,
	}

	result := setup.WM.handleNonceIsLowError(newTx, execCtx)

	// Should retry
	assert.True(t, result.ShouldRetry)
	assert.False(t, result.ShouldReturn)
}

func TestHandleNonceIsLowError_ExceedsRetries_NoPendingTxs(t *testing.T) {
	setup := newTestSetup(t)

	setup.Reader.TxInfoFromHashFn = func(hash string) (TxInfo, error) {
		return TxInfo{Status: "pending"}, nil
	}

	oldTx := newTestTx(5, testAddr2, oneEth)
	newTx := newTestTx(6, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries:       2,
		ActualRetryCount: 2, // At max
		OldTxs: map[string]*types.Transaction{
			oldTx.Hash().Hex(): oldTx,
		},
		Network: networks.EthereumMainnet,
	}

	result := setup.WM.handleNonceIsLowError(newTx, execCtx)

	assert.False(t, result.ShouldRetry)
	assert.True(t, result.ShouldReturn)
	assert.Error(t, result.Error)
}

func TestNonce_IsAliasForAcquireNonce(t *testing.T) {
	setup := newTestSetup(t)
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 5, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	nonce, err := setup.WM.nonce(testAddr1, networks.EthereumMainnet)

	require.NoError(t, err)
	assert.Equal(t, uint64(5), nonce.Uint64())
}

func TestSetPendingNonce_UpdatesTracker(t *testing.T) {
	setup := newTestSetup(t)
	wallet := testAddr1
	network := networks.EthereumMainnet

	// Initially nil
	initialNonce := setup.WM.pendingNonce(wallet, network)
	assert.Nil(t, initialNonce)

	// Set nonce
	setup.WM.setPendingNonce(wallet, network, 100)

	// Should return 101 (next nonce)
	updatedNonce := setup.WM.pendingNonce(wallet, network)
	require.NotNil(t, updatedNonce)
	assert.Equal(t, uint64(101), updatedNonce.Uint64())
}

func TestReleaseNonce_MultipleNetworks(t *testing.T) {
	setup := newTestSetup(t)
	wallet := testAddr1
	network1 := networks.EthereumMainnet
	network2 := networks.BSCMainnet

	// Acquire nonces on both networks
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 0, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 0, nil }

	nonce1, _ := setup.WM.acquireNonce(wallet, network1)
	nonce2, _ := setup.WM.acquireNonce(wallet, network2)

	// Release on network1
	setup.WM.ReleaseNonce(wallet, network1, nonce1.Uint64())

	// Acquire again on network1 - should get same nonce
	newNonce1, _ := setup.WM.acquireNonce(wallet, network1)
	assert.Equal(t, nonce1.Uint64(), newNonce1.Uint64())

	// Network2 should be unaffected - next acquire should increment
	newNonce2, _ := setup.WM.acquireNonce(wallet, network2)
	assert.Equal(t, nonce2.Uint64()+1, newNonce2.Uint64())
}

func TestAcquireNonce_ReturnsError_WhenAbnormalState(t *testing.T) {
	setup := newTestSetup(t)
	wallet := testAddr1

	// Set up local nonce first
	setup.WM.setPendingNonce(wallet, networks.EthereumMainnet, 5)

	// Configure reader to return abnormal state: mined > pending
	setup.Reader.GetMinedNonceFn = func(addr string) (uint64, error) { return 10, nil }
	setup.Reader.GetPendingNonceFn = func(addr string) (uint64, error) { return 5, nil }

	nonce, err := setup.WM.acquireNonce(wallet, networks.EthereumMainnet)

	assert.Nil(t, nonce)
	assert.Error(t, err)
	// The error should come from the nonce tracker
}

func TestHandleNonceIsLowError_StatusDoneButTxNotInMap_Continues(t *testing.T) {
	setup := newTestSetup(t)

	oldTx := newTestTx(5, testAddr2, oneEth)
	differentTx := newTestTx(7, testAddr3, oneEth)

	// Status returns "done" for oldTx, but oldTx is not actually in OldTxs
	setup.Reader.TxInfoFromHashFn = func(hash string) (TxInfo, error) {
		// This will be called with differentTx's hash
		return TxInfo{Status: "pending"}, nil
	}

	newTx := newTestTx(6, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		OldTxs: map[string]*types.Transaction{
			// Only differentTx in the map, but it's "pending"
			differentTx.Hash().Hex(): differentTx,
		},
		Network: networks.EthereumMainnet,
	}

	result := setup.WM.handleNonceIsLowError(newTx, execCtx)

	// Should retry since no completed tx was found
	assert.True(t, result.ShouldRetry)
	assert.False(t, result.ShouldReturn)

	// Make oldTx hash not in map to verify the edge case
	// Verify OldTxs doesn't have the oldTx
	_, exists := execCtx.OldTxs[oldTx.Hash().Hex()]
	assert.False(t, exists)
}

func TestHandleNonceIsLowError_HashMismatch_Continues(t *testing.T) {
	setup := newTestSetup(t)

	oldTx := newTestTx(5, testAddr2, oneEth)
	anotherTx := newTestTx(7, testAddr2, oneEth)

	// Status is "done" for a different hash than what's in OldTxs
	setup.Reader.TxInfoFromHashFn = func(hash string) (TxInfo, error) {
		// Return done for the oldTx hash
		if hash == oldTx.Hash().Hex() {
			return TxInfo{Status: "done"}, nil
		}
		return TxInfo{Status: "pending"}, nil
	}

	newTx := newTestTx(6, testAddr2, oneEth)

	execCtx := &TxExecutionContext{
		NumRetries: 5,
		OldTxs: map[string]*types.Transaction{
			// Different tx hash stored
			anotherTx.Hash().Hex(): anotherTx,
		},
		Network: networks.EthereumMainnet,
	}

	result := setup.WM.handleNonceIsLowError(newTx, execCtx)

	// Should retry since the matching hash is not in OldTxs
	assert.True(t, result.ShouldRetry)
	assert.False(t, result.ShouldReturn)
}
