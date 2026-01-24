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

// TxExecutionContext holds the state and parameters for transaction execution.
// All fields are public to allow for testing and advanced customization.
type TxExecutionContext struct {
	// Retry tracking
	ActualRetryCount int

	// Configuration
	NumRetries      int
	SleepDuration   time.Duration
	TxCheckInterval time.Duration

	// Transaction parameters
	TxType        uint8
	From, To      common.Address
	Value         *big.Int
	GasLimit      uint64
	ExtraGasLimit uint64
	Data          []byte
	Network       networks.Network

	// Gas pricing (mutable during retries)
	RetryGasPrice   float64
	ExtraGasPrice   float64
	RetryTipCap     float64
	ExtraTipCapGwei float64

	// Gas price protection limits (caller-defined)
	MaxGasPrice float64
	MaxTipCap   float64

	// Transaction state
	OldTxs     map[string]*types.Transaction
	RetryNonce *big.Int

	// InitialTx is set when resuming a pending transaction.
	// If non-nil, the first iteration skips executeTransactionAttempt and
	// goes straight to monitoring this transaction. Cleared after first use.
	InitialTx *types.Transaction

	// Hooks
	BeforeSignAndBroadcastHook Hook
	AfterSignAndBroadcastHook  Hook
	GasEstimationFailedHook    GasEstimationFailedHook
	SimulationFailedHook       SimulationFailedHook
	TxMinedHook                TxMinedHook
	ABIs                       []abi.ABI
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
		ActualRetryCount:           0,
		NumRetries:                 numRetries,
		SleepDuration:              sleepDuration,
		TxCheckInterval:            txCheckInterval,
		TxType:                     txType,
		From:                       from,
		To:                         to,
		Value:                      value,
		GasLimit:                   gasLimit,
		ExtraGasLimit:              extraGasLimit,
		RetryGasPrice:              gasPrice,
		ExtraGasPrice:              extraGasPrice,
		RetryTipCap:                tipCapGwei,
		ExtraTipCapGwei:            extraTipCapGwei,
		MaxGasPrice:                maxGasPrice,
		MaxTipCap:                  maxTipCap,
		Data:                       data,
		Network:                    network,
		OldTxs:                     make(map[string]*types.Transaction),
		RetryNonce:                 nil,
		BeforeSignAndBroadcastHook: beforeSignAndBroadcastHook,
		AfterSignAndBroadcastHook:  afterSignAndBroadcastHook,
		ABIs:                       abis,
		GasEstimationFailedHook:    gasEstimationFailedHook,
		SimulationFailedHook:       simulationFailedHook,
		TxMinedHook:                txMinedHook,
	}, nil
}

// AdjustGasPricesForSlowTx adjusts gas prices when a transaction is slow.
// Returns true if adjustment was applied, false if limits were reached.
func (ctx *TxExecutionContext) AdjustGasPricesForSlowTx(tx *types.Transaction) bool {
	if tx == nil {
		return false
	}

	// Increase gas price by configured percentage
	currentGasPrice := jarviscommon.BigToFloat(tx.GasPrice(), 9)
	newGasPrice := currentGasPrice * GasPriceIncreasePercent

	// Check if new gas price would exceed the caller-defined maximum
	if ctx.MaxGasPrice > 0 && newGasPrice > ctx.MaxGasPrice {
		// Gas price would exceed limit - stop trying
		return false
	}

	ctx.RetryGasPrice = newGasPrice

	// Increase tip cap by configured percentage
	currentTipCap := jarviscommon.BigToFloat(tx.GasTipCap(), 9)
	newTipCap := currentTipCap * TipCapIncreasePercent

	// Check if new tip cap would exceed the caller-defined maximum
	if ctx.MaxTipCap > 0 && newTipCap > ctx.MaxTipCap {
		// Tip cap would exceed limit - stop trying
		return false
	}

	ctx.RetryTipCap = newTipCap

	// Keep the same nonce
	ctx.RetryNonce = big.NewInt(int64(tx.Nonce()))

	return true
}

// IncrementRetryCountAndCheck increments retry count and checks if we've exceeded retries.
func (ctx *TxExecutionContext) IncrementRetryCountAndCheck(errorMsg string) *TxExecutionResult {
	ctx.ActualRetryCount++
	if ctx.ActualRetryCount > ctx.NumRetries {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        errors.Join(ErrEnsureTxOutOfRetries, fmt.Errorf("%s after %d retries", errorMsg, ctx.NumRetries)),
		}
	}
	return nil
}
