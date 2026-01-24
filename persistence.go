// Package walletarmy provides persistence interfaces for crash-resilient transaction management.
// Implement these interfaces to persist nonce state and in-flight transactions across restarts.
package walletarmy

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// PendingTxStatus represents the status of a pending transaction
type PendingTxStatus string

const (
	// PendingTxStatusPending means the transaction is waiting to be mined
	PendingTxStatusPending PendingTxStatus = "pending"
	// PendingTxStatusBroadcasted means the transaction has been broadcast but status is unknown
	PendingTxStatusBroadcasted PendingTxStatus = "broadcasted"
	// PendingTxStatusMined means the transaction has been mined successfully
	PendingTxStatusMined PendingTxStatus = "mined"
	// PendingTxStatusReverted means the transaction was mined but reverted
	PendingTxStatusReverted PendingTxStatus = "reverted"
	// PendingTxStatusDropped means the transaction was dropped from the mempool
	PendingTxStatusDropped PendingTxStatus = "dropped"
	// PendingTxStatusReplaced means the transaction was replaced by another with same nonce
	PendingTxStatusReplaced PendingTxStatus = "replaced"
)

// PendingTx represents a transaction that is being tracked for recovery
type PendingTx struct {
	// Hash is the transaction hash
	Hash common.Hash
	// Wallet is the sender address
	Wallet common.Address
	// ChainID is the network chain ID
	ChainID uint64
	// Nonce is the transaction nonce
	Nonce uint64
	// Status is the current status of the transaction
	Status PendingTxStatus
	// Transaction is the full transaction object (optional, for re-monitoring)
	Transaction *types.Transaction
	// Receipt is the transaction receipt (set when mined/reverted)
	Receipt *types.Receipt
	// CreatedAt is when the transaction was first tracked
	CreatedAt time.Time
	// UpdatedAt is when the transaction was last updated
	UpdatedAt time.Time
	// Metadata allows storing arbitrary application-specific data
	Metadata map[string]string
}

// NonceState represents the persisted nonce state for a wallet on a network
type NonceState struct {
	// Wallet is the wallet address
	Wallet common.Address
	// ChainID is the network chain ID
	ChainID uint64
	// LocalPendingNonce is the highest nonce we've used locally (exclusive - next nonce to use is this value)
	// If nil, no local state is tracked
	LocalPendingNonce *uint64
	// ReservedNonces are nonces that have been acquired but not yet confirmed
	// This is used to prevent reuse after crash
	ReservedNonces []uint64
	// UpdatedAt is when this state was last updated
	UpdatedAt time.Time
}

// NonceStore provides persistence for nonce tracking state.
// Implement this interface to persist nonce state across process restarts.
//
// Thread Safety: Implementations MUST be safe for concurrent use.
// The WalletManager will call these methods from multiple goroutines.
type NonceStore interface {
	// Get retrieves the nonce state for a wallet on a network.
	// Returns nil, nil if no state exists (not an error).
	Get(ctx context.Context, wallet common.Address, chainID uint64) (*NonceState, error)

	// Save persists the nonce state. Creates or updates as needed.
	Save(ctx context.Context, state *NonceState) error

	// SavePendingNonce is a convenience method to update just the pending nonce.
	// Implementations should use atomic updates if possible.
	SavePendingNonce(ctx context.Context, wallet common.Address, chainID uint64, nonce uint64) error

	// AddReservedNonce adds a nonce to the reserved set.
	// This should be called when a nonce is acquired for a transaction.
	AddReservedNonce(ctx context.Context, wallet common.Address, chainID uint64, nonce uint64) error

	// RemoveReservedNonce removes a nonce from the reserved set.
	// This should be called when a transaction is confirmed or abandoned.
	RemoveReservedNonce(ctx context.Context, wallet common.Address, chainID uint64, nonce uint64) error

	// ListAll returns all stored nonce states.
	// Used during recovery to reconcile state.
	ListAll(ctx context.Context) ([]*NonceState, error)
}

// TxStore provides persistence for in-flight transaction tracking.
// Implement this interface to track and recover pending transactions across restarts.
//
// Thread Safety: Implementations MUST be safe for concurrent use.
type TxStore interface {
	// Save persists a pending transaction. Creates or updates based on hash.
	Save(ctx context.Context, tx *PendingTx) error

	// Get retrieves a pending transaction by hash.
	// Returns nil, nil if not found (not an error).
	Get(ctx context.Context, hash common.Hash) (*PendingTx, error)

	// GetByNonce retrieves pending transactions by wallet, chainID, and nonce.
	// There may be multiple if a nonce was reused (replacement tx).
	GetByNonce(ctx context.Context, wallet common.Address, chainID uint64, nonce uint64) ([]*PendingTx, error)

	// ListPending returns all transactions with pending/broadcasted status for a wallet on a network.
	ListPending(ctx context.Context, wallet common.Address, chainID uint64) ([]*PendingTx, error)

	// ListAllPending returns all pending/broadcasted transactions across all wallets/networks.
	// Used during recovery.
	ListAllPending(ctx context.Context) ([]*PendingTx, error)

	// UpdateStatus updates the status of a transaction and optionally sets the receipt.
	UpdateStatus(ctx context.Context, hash common.Hash, status PendingTxStatus, receipt *types.Receipt) error

	// Delete removes a transaction record.
	Delete(ctx context.Context, hash common.Hash) error

	// DeleteOlderThan removes transactions older than the given duration.
	// Used for cleanup of old completed transactions.
	DeleteOlderThan(ctx context.Context, age time.Duration) (int, error)
}

// RecoveryResult contains the results of a recovery operation
type RecoveryResult struct {
	// RecoveredTxs is the number of transactions that were recovered and re-monitored
	RecoveredTxs int
	// MinedTxs is the number of transactions that were found to be already mined
	MinedTxs int
	// DroppedTxs is the number of transactions that were found to be dropped
	DroppedTxs int
	// ReconciledNonces is the number of wallet/network pairs whose nonce state was reconciled
	ReconciledNonces int
	// Errors contains any non-fatal errors encountered during recovery
	Errors []error
}

// RecoveryOptions configures the recovery process
type RecoveryOptions struct {
	// ResumeMonitoring determines whether to start monitoring recovered pending transactions.
	// If false, transactions are just marked with their current status.
	// Default: true
	ResumeMonitoring bool

	// TxCheckInterval is the interval for checking transaction status during monitoring.
	// Default: 5 seconds
	TxCheckInterval time.Duration

	// MaxConcurrentMonitors is the maximum number of transactions to monitor concurrently.
	// Default: 10
	MaxConcurrentMonitors int

	// OnTxRecovered is called for each recovered transaction.
	// Can be used to resume application-specific logic.
	OnTxRecovered func(tx *PendingTx)

	// OnTxMined is called when a recovered transaction is mined.
	OnTxMined func(tx *PendingTx, receipt *types.Receipt)

	// OnTxDropped is called when a recovered transaction is determined to be dropped.
	OnTxDropped func(tx *PendingTx)
}

// DefaultRecoveryOptions returns the default recovery options
func DefaultRecoveryOptions() RecoveryOptions {
	return RecoveryOptions{
		ResumeMonitoring:      true,
		TxCheckInterval:       5 * time.Second,
		MaxConcurrentMonitors: 10,
	}
}

// ResumeTransactionOptions configures how a pending transaction should be resumed
type ResumeTransactionOptions struct {
	// NumRetries is the maximum number of retries for gas bumping
	// Default: 9
	NumRetries int
	// SleepDuration is the duration to sleep between retries
	// Default: 5 seconds
	SleepDuration time.Duration
	// TxCheckInterval is the interval for checking transaction status
	// Default: 5 seconds
	TxCheckInterval time.Duration
	// MaxGasPrice is the maximum gas price (in gwei) to use when bumping gas
	// Default: 5x the original tx's gas price
	MaxGasPrice float64
	// MaxTipCap is the maximum tip cap (in gwei) to use when bumping gas
	// Default: 5x the original tx's tip cap
	MaxTipCap float64
	// TxMinedHook is called when the transaction is mined
	TxMinedHook TxMinedHook
}

// DefaultResumeTransactionOptions returns the default resume options
func DefaultResumeTransactionOptions() ResumeTransactionOptions {
	return ResumeTransactionOptions{
		NumRetries:      DefaultNumRetries,
		SleepDuration:   DefaultSleepDuration,
		TxCheckInterval: DefaultTxCheckInterval,
	}
}
