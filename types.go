package walletarmy

import (
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/tranvictor/jarvis/networks"
)

// Constants for transaction execution
const (
	DefaultNumRetries      = 9
	DefaultSleepDuration   = 5 * time.Second
	DefaultTxCheckInterval = 5 * time.Second

	// Gas adjustment constants
	GasPriceIncreasePercent = 1.2 // 20% increase
	TipCapIncreasePercent   = 1.1 // 10% increase
	MaxCapMultiplier        = 5.0 // Multiplier for default max gas price/tip cap when not set
)

// TxStatus represents the status of a monitored transaction
type TxStatus struct {
	Status  string
	Receipt *types.Receipt
}

// TxExecutionResult represents the outcome of a transaction execution step
type TxExecutionResult struct {
	Transaction  *types.Transaction
	Receipt      *types.Receipt
	ShouldRetry  bool
	ShouldReturn bool
	Error        error
}

// ManagerDefaults holds default configuration values that are inherited by TxRequest
type ManagerDefaults struct {
	// Retry configuration
	NumRetries      int
	SleepDuration   time.Duration
	TxCheckInterval time.Duration

	// Gas configuration
	ExtraGasLimit   uint64
	ExtraGasPrice   float64
	ExtraTipCapGwei float64
	MaxGasPrice     float64
	MaxTipCap       float64

	// Default network (if not specified in request)
	Network networks.Network

	// Default transaction type
	TxType uint8
}
