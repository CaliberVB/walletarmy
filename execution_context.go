package walletarmy

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	jarviscommon "github.com/tranvictor/jarvis/common"
	"github.com/tranvictor/jarvis/networks"
)

// TxExecutionContext holds the state and parameters for transaction execution
type TxExecutionContext struct {
	// Retry tracking
	actualRetryCount int

	// Configuration
	numRetries      int
	sleepDuration   time.Duration
	txCheckInterval time.Duration

	// Transaction parameters
	txType        uint8
	from, to      common.Address
	value         *big.Int
	gasLimit      uint64
	extraGasLimit uint64
	data          []byte
	network       networks.Network

	// Gas pricing (mutable during retries)
	retryGasPrice   float64
	extraGasPrice   float64
	retryTipCap     float64
	extraTipCapGwei float64

	// Gas price protection limits (caller-defined)
	maxGasPrice float64
	maxTipCap   float64

	// Transaction state
	oldTxs     map[string]*types.Transaction
	retryNonce *big.Int

	// Hooks
	beforeSignAndBroadcastHook Hook
	afterSignAndBroadcastHook  Hook
	gasEstimationFailedHook    GasEstimationFailedHook
	simulationFailedHook       SimulationFailedHook
	txMinedHook                TxMinedHook
	abis                       []abi.ABI
}

// NewTxExecutionContext creates a new transaction execution context
func NewTxExecutionContext(
	numRetries int,
	sleepDuration time.Duration,
	txCheckInterval time.Duration,
	txType uint8,
	from, to common.Address,
	value *big.Int,
	gasLimit uint64, extraGasLimit uint64,
	gasPrice float64, extraGasPrice float64,
	tipCapGwei float64, extraTipCapGwei float64,
	maxGasPrice float64, maxTipCap float64,
	data []byte,
	network networks.Network,
	beforeSignAndBroadcastHook Hook,
	afterSignAndBroadcastHook Hook,
	abis []abi.ABI,
	gasEstimationFailedHook GasEstimationFailedHook,
	simulationFailedHook SimulationFailedHook,
	txMinedHook TxMinedHook,
) (*TxExecutionContext, error) {
	// Validate inputs
	if numRetries < 0 {
		numRetries = 0
	}
	if sleepDuration <= 0 {
		sleepDuration = DefaultSleepDuration
	}
	if txCheckInterval <= 0 {
		txCheckInterval = DefaultTxCheckInterval
	}

	// Validate addresses
	if from == (common.Address{}) {
		return nil, ErrFromAddressZero
	}

	// Validate network
	if network == nil {
		return nil, ErrNetworkNil
	}

	// Initialize value if nil
	if value == nil {
		value = big.NewInt(0)
	}

	// Set default maxGasPrice and maxTipCap if they are 0 (to avoid infinite loop)
	if maxGasPrice == 0 {
		maxGasPrice = gasPrice * MaxCapMultiplier
	}
	if maxTipCap == 0 {
		maxTipCap = tipCapGwei * MaxCapMultiplier
	}

	return &TxExecutionContext{
		actualRetryCount:           0,
		numRetries:                 numRetries,
		sleepDuration:              sleepDuration,
		txCheckInterval:            txCheckInterval,
		txType:                     txType,
		from:                       from,
		to:                         to,
		value:                      value,
		gasLimit:                   gasLimit,
		extraGasLimit:              extraGasLimit,
		retryGasPrice:              gasPrice,
		extraGasPrice:              extraGasPrice,
		retryTipCap:                tipCapGwei,
		extraTipCapGwei:            extraTipCapGwei,
		maxGasPrice:                maxGasPrice,
		maxTipCap:                  maxTipCap,
		data:                       data,
		network:                    network,
		oldTxs:                     make(map[string]*types.Transaction),
		retryNonce:                 nil,
		beforeSignAndBroadcastHook: beforeSignAndBroadcastHook,
		afterSignAndBroadcastHook:  afterSignAndBroadcastHook,
		abis:                       abis,
		gasEstimationFailedHook:    gasEstimationFailedHook,
		simulationFailedHook:       simulationFailedHook,
		txMinedHook:                txMinedHook,
	}, nil
}

// adjustGasPricesForSlowTx adjusts gas prices when a transaction is slow
// Returns true if adjustment was applied, false if limits were reached
func (ctx *TxExecutionContext) adjustGasPricesForSlowTx(tx *types.Transaction) bool {
	if tx == nil {
		return false
	}

	// Increase gas price by configured percentage
	currentGasPrice := jarviscommon.BigToFloat(tx.GasPrice(), 9)
	newGasPrice := currentGasPrice * GasPriceIncreasePercent

	// Check if new gas price would exceed the caller-defined maximum
	if ctx.maxGasPrice > 0 && newGasPrice > ctx.maxGasPrice {
		// Gas price would exceed limit - stop trying
		return false
	}

	ctx.retryGasPrice = newGasPrice

	// Increase tip cap by configured percentage
	currentTipCap := jarviscommon.BigToFloat(tx.GasTipCap(), 9)
	newTipCap := currentTipCap * TipCapIncreasePercent

	// Check if new tip cap would exceed the caller-defined maximum
	if ctx.maxTipCap > 0 && newTipCap > ctx.maxTipCap {
		// Tip cap would exceed limit - stop trying
		return false
	}

	ctx.retryTipCap = newTipCap

	// Keep the same nonce
	ctx.retryNonce = big.NewInt(int64(tx.Nonce()))

	return true
}

// incrementRetryCountAndCheck increments retry count and checks if we've exceeded retries
func (ctx *TxExecutionContext) incrementRetryCountAndCheck(errorMsg string) *TxExecutionResult {
	ctx.actualRetryCount++
	if ctx.actualRetryCount > ctx.numRetries {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        errors.Join(ErrEnsureTxOutOfRetries, fmt.Errorf("%s after %d retries", errorMsg, ctx.numRetries)),
		}
	}
	return nil
}
