package walletarmy

import (
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/tranvictor/jarvis/accounts"
	"github.com/tranvictor/jarvis/util/account"

	"github.com/tranvictor/walletarmy/idempotency"
	"github.com/tranvictor/walletarmy/internal/circuitbreaker"
	"github.com/tranvictor/walletarmy/internal/nonce"
)

// WalletManager manages
//  1. multiple wallets and their informations in its
//     life time. It basically gives next nonce to do transaction for specific
//     wallet and specific network.
//     It queries the node to check the nonce in lazy maner, it also takes mining
//     txs into account.
//  2. multiple networks gas price. The gas price will be queried lazily prior to txs
//     and will be stored as cache for a while
//  3. txs in the context manager's life time
//  4. circuit breakers for RPC node failover
//  5. idempotency keys for preventing duplicate transactions
//  6. default configuration that TxRequest inherits
type WalletManager struct {
	// Lock for defaults access (protects the defaults struct)
	defaultsMu sync.RWMutex

	// Default configuration inherited by TxRequest
	defaults ManagerDefaults

	// Network-level locks (keyed by chainID)
	networkLocks sync.Map // map[uint64]*sync.RWMutex

	// Wallet-level locks (keyed by address)
	walletLocks sync.Map // map[common.Address]*sync.RWMutex

	// readers stores all reader instances for all networks that ever interacts
	// with accounts manager. ChainID of the network is used as the key.
	readers      sync.Map // map[uint64]*reader.EthReader
	broadcasters sync.Map // map[uint64]*broadcaster.Broadcaster
	analyzers    sync.Map // map[uint64]*txanalyzer.TxAnalyzer
	txMonitors   sync.Map // map[uint64]*monitor.TxMonitor

	// Circuit breakers for each network (keyed by chainID)
	circuitBreakers sync.Map // map[uint64]*circuitbreaker.CircuitBreaker

	// accounts keyed by address
	accounts sync.Map // map[common.Address]*account.Account

	// Nonce tracker for managing wallet nonces across networks
	nonceTracker *nonce.Tracker

	// txs map between (address, network, nonce) => tx
	// Protected by wallet-level locks
	txs sync.Map // map[common.Address]map[uint64]map[uint64]*types.Transaction

	// gasPrices map between network => gasinfo
	// Protected by network-level locks
	gasSettings sync.Map // map[uint64]*GasInfo

	// Idempotency store for preventing duplicate transactions
	idempotencyStore idempotency.Store
}

// NewWalletManager creates a new WalletManager with optional configuration
func NewWalletManager(opts ...WalletManagerOption) *WalletManager {
	wm := &WalletManager{
		nonceTracker: nonce.NewTracker(),
	}

	for _, opt := range opts {
		opt(wm)
	}

	return wm
}

// Defaults returns the current default configuration
func (wm *WalletManager) Defaults() ManagerDefaults {
	wm.defaultsMu.RLock()
	defer wm.defaultsMu.RUnlock()
	return wm.defaults
}

// SetDefaults updates the default configuration
func (wm *WalletManager) SetDefaults(defaults ManagerDefaults) {
	wm.defaultsMu.Lock()
	defer wm.defaultsMu.Unlock()
	wm.defaults = defaults
}

// IdempotencyStore returns the configured idempotency store, or nil if not configured
func (wm *WalletManager) IdempotencyStore() idempotency.Store {
	return wm.idempotencyStore
}

// getNetworkLock returns the lock for a specific network, creating it if necessary
func (wm *WalletManager) getNetworkLock(chainID uint64) *sync.RWMutex {
	lock, _ := wm.networkLocks.LoadOrStore(chainID, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

// getWalletLock returns the lock for a specific wallet, creating it if necessary
func (wm *WalletManager) getWalletLock(wallet common.Address) *sync.RWMutex {
	lock, _ := wm.walletLocks.LoadOrStore(wallet, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

// getCircuitBreaker returns the circuit breaker for a network, creating one if necessary
func (wm *WalletManager) getCircuitBreaker(chainID uint64) *circuitbreaker.CircuitBreaker {
	cb, _ := wm.circuitBreakers.LoadOrStore(chainID, circuitbreaker.New(circuitbreaker.DefaultConfig()))
	return cb.(*circuitbreaker.CircuitBreaker)
}

// SetAccount registers an account with the wallet manager
func (wm *WalletManager) SetAccount(acc *account.Account) {
	wm.accounts.Store(acc.Address(), acc)
}

// UnlockAccount unlocks an account from the jarvis wallet store and registers it
func (wm *WalletManager) UnlockAccount(addr common.Address) (*account.Account, error) {
	accDesc, err := accounts.GetAccount(addr.Hex())
	if err != nil {
		return nil, fmt.Errorf("wallet %s doesn't exist in jarvis", addr.Hex())
	}
	acc, err := accounts.UnlockAccount(accDesc)
	if err != nil {
		return nil, fmt.Errorf("unlocking wallet failed: %w", err)
	}
	wm.SetAccount(acc)
	return acc, nil
}

// Account returns the account for the given wallet address
func (wm *WalletManager) Account(wallet common.Address) *account.Account {
	if acc, ok := wm.accounts.Load(wallet); ok {
		return acc.(*account.Account)
	}
	return nil
}
