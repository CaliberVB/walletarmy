package walletarmy

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/txanalyzer"
	"github.com/tranvictor/jarvis/util/account"
	"github.com/tranvictor/jarvis/util/broadcaster"
	"github.com/tranvictor/jarvis/util/reader"
	"github.com/tranvictor/walletarmy/internal/circuitbreaker"
)

// Manager defines the interface for wallet management operations.
// This interface allows for easy mocking in tests and provides a stable API contract.
type Manager interface {
	// Account Management
	SetAccount(acc *account.Account)
	UnlockAccount(addr common.Address) (*account.Account, error)
	Account(wallet common.Address) *account.Account

	// Network Infrastructure
	Reader(network networks.Network) (*reader.EthReader, error)
	Broadcaster(network networks.Network) (*broadcaster.Broadcaster, error)
	Analyzer(network networks.Network) (*txanalyzer.TxAnalyzer, error)

	// Gas Settings
	GasSetting(network networks.Network) (*GasInfo, error)

	// Nonce Management
	// ReleaseNonce releases a previously acquired nonce that was not used.
	ReleaseNonce(wallet common.Address, network networks.Network, nonce uint64)

	// Circuit Breaker
	GetCircuitBreakerStats(network networks.Network) circuitbreaker.Stats
	ResetCircuitBreaker(network networks.Network)
	RecordNetworkSuccess(network networks.Network)
	RecordNetworkFailure(network networks.Network)

	// Idempotency
	IdempotencyStore() IdempotencyStore

	// Default Configuration
	Defaults() ManagerDefaults
	SetDefaults(defaults ManagerDefaults)

	// Transaction Building
	BuildTx(
		txType uint8,
		from, to common.Address,
		nonce *big.Int,
		value *big.Int,
		gasLimit uint64,
		extraGasLimit uint64,
		gasPrice float64,
		extraGasPrice float64,
		tipCapGwei float64,
		extraTipCapGwei float64,
		data []byte,
		network networks.Network,
	) (*types.Transaction, error)

	// Transaction Signing
	SignTx(
		wallet common.Address,
		tx *types.Transaction,
		network networks.Network,
	) (signedAddr common.Address, signedTx *types.Transaction, err error)

	// Transaction Broadcasting
	BroadcastTx(tx *types.Transaction) (hash string, broadcasted bool, err BroadcastError)
	BroadcastTxSync(tx *types.Transaction) (receipt *types.Receipt, err error)

	// Transaction Monitoring
	// MonitorTx is deprecated, use MonitorTxContext instead for better cancellation support.
	MonitorTx(tx *types.Transaction, network networks.Network, txCheckInterval time.Duration) <-chan TxStatus
	// MonitorTxContext is a context-aware version of MonitorTx that supports cancellation.
	MonitorTxContext(ctx context.Context, tx *types.Transaction, network networks.Network, txCheckInterval time.Duration) <-chan TxStatus

	// High-Level Transaction Execution
	EnsureTx(
		txType uint8,
		from, to common.Address,
		value *big.Int,
		gasLimit uint64,
		extraGasLimit uint64,
		gasPrice float64,
		extraGasPrice float64,
		tipCapGwei float64,
		extraTipCapGwei float64,
		data []byte,
		network networks.Network,
	) (*types.Transaction, *types.Receipt, error)

	EnsureTxWithHooks(
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
	) (*types.Transaction, *types.Receipt, error)

	EnsureTxWithHooksContext(
		ctx context.Context,
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
	) (*types.Transaction, *types.Receipt, error)

	// Builder Pattern Entry Point
	R() *TxRequest
}

// Compile-time check that WalletManager implements Manager
var _ Manager = (*WalletManager)(nil)
