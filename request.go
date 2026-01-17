package walletarmy

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/tranvictor/jarvis/networks"
)

// TxRequest represents a transaction request with builder pattern
type TxRequest struct {
	wm *WalletManager

	numRetries      int
	sleepDuration   time.Duration
	txCheckInterval time.Duration

	// Transaction parameters
	txType          uint8
	from, to        common.Address
	value           *big.Int
	gasLimit        uint64
	extraGasLimit   uint64
	gasPrice        float64
	extraGasPrice   float64
	tipCapGwei      float64
	extraTipCapGwei float64
	maxGasPrice     float64
	maxTipCap       float64
	data            []byte
	network         networks.Network

	// Hooks
	beforeSignAndBroadcastHook Hook
	afterSignAndBroadcastHook  Hook
	gasEstimationFailedHook    GasEstimationFailedHook
	simulationFailedHook       SimulationFailedHook
	txMinedHook                TxMinedHook
	abis                       []abi.ABI

	// Idempotency key for preventing duplicate transactions
	idempotencyKey string
}

// R creates a new transaction request (similar to go-resty's R() method).
// The request inherits default configuration from the WalletManager.
func (wm *WalletManager) R() *TxRequest {
	// Use Defaults() to safely read with lock
	defaults := wm.Defaults()

	// Determine default network
	network := defaults.Network
	if network == nil {
		network = networks.EthereumMainnet
	}

	return &TxRequest{
		wm:              wm,
		value:           big.NewInt(0),
		network:         network,
		numRetries:      defaults.NumRetries,
		sleepDuration:   defaults.SleepDuration,
		txCheckInterval: defaults.TxCheckInterval,
		txType:          defaults.TxType,
		extraGasLimit:   defaults.ExtraGasLimit,
		extraGasPrice:   defaults.ExtraGasPrice,
		extraTipCapGwei: defaults.ExtraTipCapGwei,
		maxGasPrice:     defaults.MaxGasPrice,
		maxTipCap:       defaults.MaxTipCap,
	}
}

// SetNumRetries sets the number of retries
func (r *TxRequest) SetNumRetries(numRetries int) *TxRequest {
	r.numRetries = numRetries
	return r
}

// SetSleepDuration sets the sleep duration
func (r *TxRequest) SetSleepDuration(sleepDuration time.Duration) *TxRequest {
	r.sleepDuration = sleepDuration
	return r
}

// SetTxCheckInterval sets the transaction check interval
func (r *TxRequest) SetTxCheckInterval(txCheckInterval time.Duration) *TxRequest {
	r.txCheckInterval = txCheckInterval
	return r
}

// SetTxType sets the transaction type
func (r *TxRequest) SetTxType(txType uint8) *TxRequest {
	r.txType = txType
	return r
}

// SetFrom sets the from address
func (r *TxRequest) SetFrom(from common.Address) *TxRequest {
	r.from = from
	return r
}

// SetTo sets the to address
func (r *TxRequest) SetTo(to common.Address) *TxRequest {
	r.to = to
	return r
}

// SetValue sets the transaction value
func (r *TxRequest) SetValue(value *big.Int) *TxRequest {
	if value != nil {
		r.value = value
	}
	return r
}

// SetGasLimit sets the gas limit
func (r *TxRequest) SetGasLimit(gasLimit uint64) *TxRequest {
	r.gasLimit = gasLimit
	return r
}

// SetExtraGasLimit sets the extra gas limit
func (r *TxRequest) SetExtraGasLimit(extraGasLimit uint64) *TxRequest {
	r.extraGasLimit = extraGasLimit
	return r
}

// SetGasPrice sets the gas price
func (r *TxRequest) SetGasPrice(gasPrice float64) *TxRequest {
	r.gasPrice = gasPrice
	return r
}

// SetExtraGasPrice sets the extra gas price
func (r *TxRequest) SetExtraGasPrice(extraGasPrice float64) *TxRequest {
	r.extraGasPrice = extraGasPrice
	return r
}

// SetTipCapGwei sets the tip cap in gwei
func (r *TxRequest) SetTipCapGwei(tipCapGwei float64) *TxRequest {
	r.tipCapGwei = tipCapGwei
	return r
}

// SetExtraTipCapGwei sets the extra tip cap in gwei
func (r *TxRequest) SetExtraTipCapGwei(extraTipCapGwei float64) *TxRequest {
	r.extraTipCapGwei = extraTipCapGwei
	return r
}

// SetMaxGasPrice sets the maximum gas price limit
func (r *TxRequest) SetMaxGasPrice(maxGasPrice float64) *TxRequest {
	r.maxGasPrice = maxGasPrice
	return r
}

// SetMaxTipCap sets the maximum tip cap limit
func (r *TxRequest) SetMaxTipCap(maxTipCap float64) *TxRequest {
	r.maxTipCap = maxTipCap
	return r
}

// SetData sets the transaction data
func (r *TxRequest) SetData(data []byte) *TxRequest {
	r.data = data
	return r
}

// SetNetwork sets the network
func (r *TxRequest) SetNetwork(network networks.Network) *TxRequest {
	r.network = network
	return r
}

// SetAbis sets the abis
func (r *TxRequest) SetAbis(abis ...abi.ABI) *TxRequest {
	r.abis = abis
	return r
}

// SetBeforeSignAndBroadcastHook sets the hook to be called before signing and broadcasting
func (r *TxRequest) SetBeforeSignAndBroadcastHook(hook Hook) *TxRequest {
	r.beforeSignAndBroadcastHook = hook
	return r
}

// SetAfterSignAndBroadcastHook sets the hook to be called after signing and broadcasting
func (r *TxRequest) SetAfterSignAndBroadcastHook(hook Hook) *TxRequest {
	r.afterSignAndBroadcastHook = hook
	return r
}

// SetGasEstimationFailedHook sets the hook to be called when gas estimation fails
func (r *TxRequest) SetGasEstimationFailedHook(hook GasEstimationFailedHook) *TxRequest {
	r.gasEstimationFailedHook = hook
	return r
}

// SetSimulationFailedHook sets the hook to be called when eth_call simulation fails.
// This hook is called when the transaction would revert, allowing the caller to decide
// whether to retry or handle the error.
func (r *TxRequest) SetSimulationFailedHook(hook SimulationFailedHook) *TxRequest {
	r.simulationFailedHook = hook
	return r
}

// SetTxMinedHook sets the hook to be called when a transaction is mined.
// This hook is called for both successful and reverted transactions.
func (r *TxRequest) SetTxMinedHook(hook TxMinedHook) *TxRequest {
	r.txMinedHook = hook
	return r
}

// SetIdempotencyKey sets a unique key to prevent duplicate transaction submissions.
// If the same key is used again, the previous transaction result will be returned
// instead of submitting a new transaction.
// Requires an IdempotencyStore to be configured on the WalletManager.
func (r *TxRequest) SetIdempotencyKey(key string) *TxRequest {
	r.idempotencyKey = key
	return r
}

// Execute executes the transaction request using a background context.
// For production use, prefer ExecuteContext to allow cancellation.
func (r *TxRequest) Execute() (*types.Transaction, *types.Receipt, error) {
	return r.ExecuteContext(context.Background())
}

// ExecuteContext executes the transaction request with context support.
// The context allows the caller to cancel long-running retry loops.
func (r *TxRequest) ExecuteContext(ctx context.Context) (*types.Transaction, *types.Receipt, error) {
	// Validate required fields before starting
	if r.from == (common.Address{}) {
		return nil, nil, ErrFromAddressZero
	}
	if r.network == nil {
		return nil, nil, ErrNetworkNil
	}

	// Handle idempotency if a key is provided and store is configured
	if r.idempotencyKey != "" && r.wm.idempotencyStore != nil {
		return r.executeWithIdempotency(ctx)
	}

	return r.executeInternal(ctx)
}

// executeWithIdempotency handles idempotent execution
func (r *TxRequest) executeWithIdempotency(ctx context.Context) (*types.Transaction, *types.Receipt, error) {
	store := r.wm.idempotencyStore

	// Try to get existing record
	existing, err := store.Get(r.idempotencyKey)
	if err == nil {
		// Record exists - check status
		switch existing.Status {
		case IdempotencyStatusConfirmed:
			// Already completed successfully
			return existing.Transaction, existing.Receipt, nil
		case IdempotencyStatusFailed:
			// Previously failed - return the error
			return existing.Transaction, existing.Receipt, existing.Error
		case IdempotencyStatusPending, IdempotencyStatusSubmitted:
			// Still in progress - return duplicate error
			return existing.Transaction, existing.Receipt, ErrDuplicateIdempotencyKey
		}
	}

	// Create new record
	record, err := store.Create(r.idempotencyKey)
	if err == ErrDuplicateIdempotencyKey {
		// Race condition - another request created the record
		return record.Transaction, record.Receipt, ErrDuplicateIdempotencyKey
	}
	if err != nil {
		return nil, nil, err
	}

	// Execute the transaction
	tx, receipt, txErr := r.executeInternal(ctx)

	// Update the record with results
	record.Transaction = tx
	record.Receipt = receipt
	record.Error = txErr

	if tx != nil {
		record.TxHash = tx.Hash()
	}

	if txErr != nil {
		record.Status = IdempotencyStatusFailed
	} else if receipt != nil {
		record.Status = IdempotencyStatusConfirmed
	} else {
		record.Status = IdempotencyStatusSubmitted
	}

	// Best effort update - don't fail the transaction if update fails
	_ = store.Update(record)

	return tx, receipt, txErr
}

// executeInternal performs the actual transaction execution
func (r *TxRequest) executeInternal(ctx context.Context) (*types.Transaction, *types.Receipt, error) {
	return r.wm.EnsureTxWithHooksContext(
		ctx,
		r.numRetries,
		r.sleepDuration,
		r.txCheckInterval,
		r.txType,
		r.from,
		r.to,
		r.value,
		r.gasLimit, r.extraGasLimit,
		r.gasPrice, r.extraGasPrice,
		r.tipCapGwei, r.extraTipCapGwei,
		r.maxGasPrice, r.maxTipCap,
		r.data,
		r.network,
		r.beforeSignAndBroadcastHook,
		r.afterSignAndBroadcastHook,
		r.abis,
		r.gasEstimationFailedHook,
		r.simulationFailedHook,
		r.txMinedHook,
	)
}
