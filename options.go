package walletarmy

import (
	"time"

	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/walletarmy/idempotency"
)

// WalletManagerOption is a function that configures a WalletManager
type WalletManagerOption func(*WalletManager)

// WithIdempotencyStore sets a custom idempotency store
func WithIdempotencyStore(store idempotency.Store) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.idempotencyStore = store
	}
}

// WithDefaultIdempotencyStore sets up an in-memory idempotency store with the given TTL
func WithDefaultIdempotencyStore(ttl time.Duration) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.idempotencyStore = idempotency.NewInMemoryStore(ttl)
	}
}

// WithDefaultNumRetries sets the default number of retries for transactions
func WithDefaultNumRetries(numRetries int) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.NumRetries = numRetries
	}
}

// WithDefaultSleepDuration sets the default sleep duration between retries
func WithDefaultSleepDuration(duration time.Duration) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.SleepDuration = duration
	}
}

// WithDefaultTxCheckInterval sets the default transaction check interval
func WithDefaultTxCheckInterval(interval time.Duration) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.TxCheckInterval = interval
	}
}

// WithDefaultExtraGasLimit sets the default extra gas limit added to estimates
func WithDefaultExtraGasLimit(extraGasLimit uint64) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.ExtraGasLimit = extraGasLimit
	}
}

// WithDefaultExtraGasPrice sets the default extra gas price added to suggestions
func WithDefaultExtraGasPrice(extraGasPrice float64) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.ExtraGasPrice = extraGasPrice
	}
}

// WithDefaultExtraTipCap sets the default extra tip cap added to suggestions
func WithDefaultExtraTipCap(extraTipCap float64) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.ExtraTipCapGwei = extraTipCap
	}
}

// WithDefaultMaxGasPrice sets the default maximum gas price protection limit
func WithDefaultMaxGasPrice(maxGasPrice float64) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.MaxGasPrice = maxGasPrice
	}
}

// WithDefaultMaxTipCap sets the default maximum tip cap protection limit
func WithDefaultMaxTipCap(maxTipCap float64) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.MaxTipCap = maxTipCap
	}
}

// WithDefaultNetwork sets the default network for transactions
func WithDefaultNetwork(network networks.Network) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.Network = network
	}
}

// WithDefaultTxType sets the default transaction type
func WithDefaultTxType(txType uint8) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults.TxType = txType
	}
}

// WithDefaults sets all default configuration at once
func WithDefaults(defaults ManagerDefaults) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.defaults = defaults
	}
}

// WithReaderFactory sets a custom reader factory for testing or alternative implementations
func WithReaderFactory(factory NetworkReaderFactory) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.readerFactory = factory
	}
}

// WithBroadcasterFactory sets a custom broadcaster factory for testing or alternative implementations
func WithBroadcasterFactory(factory NetworkBroadcasterFactory) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.broadcasterFactory = factory
	}
}

// WithTxMonitorFactory sets a custom tx monitor factory for testing or alternative implementations
func WithTxMonitorFactory(factory NetworkTxMonitorFactory) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.txMonitorFactory = factory
	}
}

// WithNonceStore sets a custom nonce store for persisting nonce state across restarts.
// This enables crash recovery for nonce tracking.
func WithNonceStore(store NonceStore) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.nonceStore = store
	}
}

// WithTxStore sets a custom transaction store for tracking in-flight transactions.
// This enables crash recovery for pending transactions.
func WithTxStore(store TxStore) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.txStore = store
	}
}

// WithNetworkResolver sets a custom network resolver for looking up networks by chain ID.
// This is required when using custom networks that are not built into jarvis
// (e.g., private chains, custom testnets).
//
// The resolver is used by:
//   - Crash recovery (to resume pending transactions)
//   - BroadcastTx and BroadcastTxSync (to determine which network to broadcast to)
//
// If not set, defaults to jarvis networks.GetNetworkByID which supports standard
// EVM networks (Ethereum, Polygon, Arbitrum, Optimism, etc.).
//
// Example for custom networks:
//
//	wm := walletarmy.NewWalletManager(
//	    walletarmy.WithNetworkResolver(func(chainID uint64) (networks.Network, error) {
//	        switch chainID {
//	        case 12345:
//	            return myCustomNetwork, nil
//	        default:
//	            return networks.GetNetworkByID(chainID) // fallback to jarvis
//	        }
//	    }),
//	)
func WithNetworkResolver(resolver NetworkResolver) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.networkResolver = resolver
	}
}
