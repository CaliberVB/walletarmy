package walletarmy

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/KyberNetwork/logger"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/tranvictor/jarvis/accounts"
	jarviscommon "github.com/tranvictor/jarvis/common"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/txanalyzer"
	"github.com/tranvictor/jarvis/util"
	"github.com/tranvictor/jarvis/util/account"
	"github.com/tranvictor/jarvis/util/broadcaster"
	"github.com/tranvictor/jarvis/util/monitor"
	"github.com/tranvictor/jarvis/util/reader"

	"github.com/tranvictor/walletarmy/internal/circuitbreaker"
)

const (
	DefaultNumRetries      = 9
	DefaultSleepDuration   = 5 * time.Second
	DefaultTxCheckInterval = 5 * time.Second

	// Gas adjustment constants
	GasPriceIncreasePercent = 1.2 // 20% increase
	TipCapIncreasePercent   = 1.1 // 10% increase
	MaxCapMultiplier        = 5.0 // Multiplier for default max gas price/tip cap when not set
)

var (
	ErrEstimateGasFailed    = fmt.Errorf("estimate gas failed")
	ErrAcquireNonceFailed   = fmt.Errorf("acquire nonce failed")
	ErrGetGasSettingFailed  = fmt.Errorf("get gas setting failed")
	ErrEnsureTxOutOfRetries = fmt.Errorf("ensure tx out of retries")
	ErrGasPriceLimitReached = fmt.Errorf("gas price protection limit reached")
	ErrFromAddressZero      = fmt.Errorf("from address cannot be zero")
	ErrNetworkNil           = fmt.Errorf("network cannot be nil")
	ErrSimulatedTxReverted  = fmt.Errorf("tx will be reverted")
	ErrSimulatedTxFailed    = fmt.Errorf("couldn't simulate tx at pending state")
	ErrCircuitBreakerOpen   = fmt.Errorf("circuit breaker is open: network temporarily unavailable")
)

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

	// nonces map between (address, network) => last signed nonce (not mined nonces)
	// Protected by wallet-level locks
	pendingNonces sync.Map // map[common.Address]map[uint64]*big.Int

	// txs map between (address, network, nonce) => tx
	// Protected by wallet-level locks
	txs sync.Map // map[common.Address]map[uint64]map[uint64]*types.Transaction

	// gasPrices map between network => gasinfo
	// Protected by network-level locks
	gasSettings sync.Map // map[uint64]*GasInfo

	// Idempotency store for preventing duplicate transactions
	idempotencyStore IdempotencyStore
}

// WalletManagerOption is a function that configures a WalletManager
type WalletManagerOption func(*WalletManager)

// WithIdempotencyStore sets a custom idempotency store
func WithIdempotencyStore(store IdempotencyStore) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.idempotencyStore = store
	}
}

// WithDefaultIdempotencyStore sets up an in-memory idempotency store with the given TTL
func WithDefaultIdempotencyStore(ttl time.Duration) WalletManagerOption {
	return func(wm *WalletManager) {
		wm.idempotencyStore = NewInMemoryIdempotencyStore(ttl)
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

// NewWalletManager creates a new WalletManager with optional configuration
func NewWalletManager(opts ...WalletManagerOption) *WalletManager {
	wm := &WalletManager{}

	for _, opt := range opts {
		opt(wm)
	}

	return wm
}

// getCircuitBreaker returns the circuit breaker for a network, creating one if necessary
func (wm *WalletManager) getCircuitBreaker(chainID uint64) *circuitbreaker.CircuitBreaker {
	cb, _ := wm.circuitBreakers.LoadOrStore(chainID, circuitbreaker.New(circuitbreaker.DefaultConfig()))
	return cb.(*circuitbreaker.CircuitBreaker)
}

// GetCircuitBreakerStats returns the circuit breaker statistics for a network
func (wm *WalletManager) GetCircuitBreakerStats(network networks.Network) circuitbreaker.Stats {
	return wm.getCircuitBreaker(network.GetChainID()).Stats()
}

// ResetCircuitBreaker resets the circuit breaker for a network
func (wm *WalletManager) ResetCircuitBreaker(network networks.Network) {
	wm.getCircuitBreaker(network.GetChainID()).Reset()
}

// IdempotencyStore returns the configured idempotency store, or nil if not configured
func (wm *WalletManager) IdempotencyStore() IdempotencyStore {
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

func (wm *WalletManager) SetAccount(acc *account.Account) {
	wm.accounts.Store(acc.Address(), acc)
}

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

func (wm *WalletManager) Account(wallet common.Address) *account.Account {
	if acc, ok := wm.accounts.Load(wallet); ok {
		return acc.(*account.Account)
	}
	return nil
}

func (wm *WalletManager) setTx(wallet common.Address, network networks.Network, tx *types.Transaction) {
	lock := wm.getWalletLock(wallet)
	lock.Lock()
	defer lock.Unlock()

	// Get or create wallet's network map
	networkMapRaw, _ := wm.txs.LoadOrStore(wallet, make(map[uint64]map[uint64]*types.Transaction))
	networkMap := networkMapRaw.(map[uint64]map[uint64]*types.Transaction)

	// Get or create network's nonce map
	if networkMap[network.GetChainID()] == nil {
		networkMap[network.GetChainID()] = map[uint64]*types.Transaction{}
	}

	networkMap[network.GetChainID()][tx.Nonce()] = tx
}

func (wm *WalletManager) getBroadcaster(network networks.Network) *broadcaster.Broadcaster {
	if b, ok := wm.broadcasters.Load(network.GetChainID()); ok {
		return b.(*broadcaster.Broadcaster)
	}
	return nil
}

// Broadcaster returns the broadcaster for the given network.
// Returns an error if the network could not be initialized or if the circuit breaker is open.
func (wm *WalletManager) Broadcaster(network networks.Network) (*broadcaster.Broadcaster, error) {
	// Check circuit breaker
	cb := wm.getCircuitBreaker(network.GetChainID())
	if !cb.Allow() {
		return nil, fmt.Errorf("%w for network %s", ErrCircuitBreakerOpen, network)
	}

	b := wm.getBroadcaster(network)
	if b == nil {
		err := wm.initNetwork(network)
		if err != nil {
			cb.RecordFailure()
			return nil, fmt.Errorf("couldn't init broadcaster for network %s: %w", network, err)
		}
		b = wm.getBroadcaster(network)
	}
	cb.RecordSuccess()
	return b, nil
}

func (wm *WalletManager) getReader(network networks.Network) *reader.EthReader {
	if r, ok := wm.readers.Load(network.GetChainID()); ok {
		return r.(*reader.EthReader)
	}
	return nil
}

// Reader returns the reader for the given network.
// Returns an error if the network could not be initialized or if the circuit breaker is open.
func (wm *WalletManager) Reader(network networks.Network) (*reader.EthReader, error) {
	// Check circuit breaker
	cb := wm.getCircuitBreaker(network.GetChainID())
	if !cb.Allow() {
		return nil, fmt.Errorf("%w for network %s", ErrCircuitBreakerOpen, network)
	}

	r := wm.getReader(network)
	if r == nil {
		err := wm.initNetwork(network)
		if err != nil {
			cb.RecordFailure()
			return nil, fmt.Errorf("couldn't init reader for network %s: %w", network, err)
		}
		r = wm.getReader(network)
	}
	cb.RecordSuccess()
	return r, nil
}

// RecordNetworkSuccess records a successful network operation for the circuit breaker
func (wm *WalletManager) RecordNetworkSuccess(network networks.Network) {
	wm.getCircuitBreaker(network.GetChainID()).RecordSuccess()
}

// RecordNetworkFailure records a failed network operation for the circuit breaker
func (wm *WalletManager) RecordNetworkFailure(network networks.Network) {
	wm.getCircuitBreaker(network.GetChainID()).RecordFailure()
}

func (wm *WalletManager) getAnalyzer(network networks.Network) *txanalyzer.TxAnalyzer {
	if a, ok := wm.analyzers.Load(network.GetChainID()); ok {
		return a.(*txanalyzer.TxAnalyzer)
	}
	return nil
}

func (wm *WalletManager) getTxMonitor(network networks.Network) *monitor.TxMonitor {
	if m, ok := wm.txMonitors.Load(network.GetChainID()); ok {
		return m.(*monitor.TxMonitor)
	}
	return nil
}

// Analyzer returns the transaction analyzer for the given network.
// Returns an error if the network could not be initialized or if the circuit breaker is open.
func (wm *WalletManager) Analyzer(network networks.Network) (*txanalyzer.TxAnalyzer, error) {
	// Check circuit breaker
	cb := wm.getCircuitBreaker(network.GetChainID())
	if !cb.Allow() {
		return nil, fmt.Errorf("%w for network %s", ErrCircuitBreakerOpen, network)
	}

	a := wm.getAnalyzer(network)
	if a == nil {
		err := wm.initNetwork(network)
		if err != nil {
			cb.RecordFailure()
			return nil, fmt.Errorf("couldn't init analyzer for network %s: %w", network, err)
		}
		a = wm.getAnalyzer(network)
	}
	cb.RecordSuccess()
	return a, nil
}

func (wm *WalletManager) initNetwork(network networks.Network) (err error) {
	chainID := network.GetChainID()
	lock := wm.getNetworkLock(chainID)
	lock.Lock()
	defer lock.Unlock()

	// Check if reader exists, create if not
	var r *reader.EthReader
	if existing, ok := wm.readers.Load(chainID); ok {
		r = existing.(*reader.EthReader)
	} else {
		r, err = util.EthReader(network)
		if err != nil {
			return err
		}
		wm.readers.Store(chainID, r)
	}

	// Check if analyzer exists, create if not
	if _, ok := wm.analyzers.Load(chainID); !ok {
		analyzer := txanalyzer.NewGenericAnalyzer(r, network)
		wm.analyzers.Store(chainID, analyzer)
	}

	// Check if broadcaster exists, create if not
	if _, ok := wm.broadcasters.Load(chainID); !ok {
		b, err := util.EthBroadcaster(network)
		if err != nil {
			return err
		}
		wm.broadcasters.Store(chainID, b)
	}

	// Check if tx monitor exists, create if not
	if _, ok := wm.txMonitors.Load(chainID); !ok {
		txMon := monitor.NewGenericTxMonitor(r)
		wm.txMonitors.Store(chainID, txMon)
	}

	return nil
}

// getOrCreateNonceMap returns the nonce map for a wallet, creating it if necessary.
// MUST be called with wallet lock held.
func (wm *WalletManager) getOrCreateNonceMap(wallet common.Address) map[uint64]*big.Int {
	noncesRaw, _ := wm.pendingNonces.LoadOrStore(wallet, make(map[uint64]*big.Int))
	return noncesRaw.(map[uint64]*big.Int)
}

// setPendingNonceUnlocked sets the pending nonce. MUST be called with wallet lock held.
func (wm *WalletManager) setPendingNonceUnlocked(wallet common.Address, network networks.Network, nonce uint64) {
	walletNonces := wm.getOrCreateNonceMap(wallet)
	oldNonce := walletNonces[network.GetChainID()]
	if oldNonce != nil && oldNonce.Cmp(big.NewInt(int64(nonce))) >= 0 {
		logger.WithFields(logger.Fields{
			"wallet":    wallet.Hex(),
			"network":   network.GetName(),
			"chain_id":  network.GetChainID(),
			"new_nonce": nonce,
			"old_nonce": oldNonce.Uint64(),
		}).Debug("setPendingNonce skipped: new nonce not higher than existing")
		return
	}

	var oldNonceVal string
	if oldNonce != nil {
		oldNonceVal = oldNonce.String()
	} else {
		oldNonceVal = "nil"
	}
	logger.WithFields(logger.Fields{
		"wallet":    wallet.Hex(),
		"network":   network.GetName(),
		"chain_id":  network.GetChainID(),
		"new_nonce": nonce,
		"old_nonce": oldNonceVal,
	}).Debug("setPendingNonce: updating local pending nonce")

	walletNonces[network.GetChainID()] = big.NewInt(int64(nonce))
}

func (wm *WalletManager) setPendingNonce(wallet common.Address, network networks.Network, nonce uint64) {
	lock := wm.getWalletLock(wallet)
	lock.Lock()
	defer lock.Unlock()
	wm.setPendingNonceUnlocked(wallet, network, nonce)
}

// getPendingNonceUnlocked returns the next nonce to use. MUST be called with wallet lock held.
// Returns nil if no local nonce is tracked.
func (wm *WalletManager) getPendingNonceUnlocked(wallet common.Address, network networks.Network) *big.Int {
	noncesRaw, ok := wm.pendingNonces.Load(wallet)
	if !ok {
		return nil
	}
	walletPendingNonces := noncesRaw.(map[uint64]*big.Int)
	result := walletPendingNonces[network.GetChainID()]
	if result != nil {
		// when there is a pending nonce, we add 1 to get the next nonce
		result = big.NewInt(0).Add(result, big.NewInt(1))
	}
	return result
}

func (wm *WalletManager) pendingNonce(wallet common.Address, network networks.Network) *big.Int {
	lock := wm.getWalletLock(wallet)
	lock.RLock()
	defer lock.RUnlock()
	return wm.getPendingNonceUnlocked(wallet, network)
}

// acquireNonce atomically determines and reserves the next nonce for a transaction.
// This prevents race conditions where multiple concurrent transactions could get the same nonce.
// The nonce is reserved immediately, so even if the transaction fails, subsequent calls will get
// a different nonce. Use ReleaseNonce if you need to release an unused nonce.
//
// Logic:
//  1. Get remote pending nonce and mined nonce from the network
//  2. Compare with local pending nonce to determine the correct next nonce
//  3. Atomically reserve the nonce by incrementing local pending nonce
//  4. Return the reserved nonce
func (wm *WalletManager) acquireNonce(wallet common.Address, network networks.Network) (*big.Int, error) {
	// Get remote nonces first (before acquiring lock to avoid holding lock during network calls)
	r, err := wm.Reader(network)
	if err != nil {
		return nil, fmt.Errorf("couldn't get reader: %w", err)
	}

	minedNonce, err := r.GetMinedNonce(wallet.Hex())
	if err != nil {
		return nil, fmt.Errorf("couldn't get mined nonce in context manager: %s", err)
	}

	remotePendingNonce, err := r.GetPendingNonce(wallet.Hex())
	if err != nil {
		return nil, fmt.Errorf("couldn't get remote pending nonce in context manager: %s", err)
	}

	// Now acquire lock and atomically determine + reserve the nonce
	lock := wm.getWalletLock(wallet)
	lock.Lock()
	defer lock.Unlock()

	localPendingNonceBig := wm.getPendingNonceUnlocked(wallet, network)

	var nextNonce uint64
	var decisionReason string

	if localPendingNonceBig == nil {
		// First transaction for this wallet/network in this session
		// Use the max of mined and remote pending nonce
		if remotePendingNonce > minedNonce {
			nextNonce = remotePendingNonce
			decisionReason = "first tx, using remote pending (higher than mined)"
		} else {
			nextNonce = minedNonce
			decisionReason = "first tx, using mined nonce"
		}
	} else {
		localPendingNonce := localPendingNonceBig.Uint64()

		hasPendingTxsOnNodes := minedNonce < remotePendingNonce
		if !hasPendingTxsOnNodes {
			if minedNonce > remotePendingNonce {
				logger.WithFields(logger.Fields{
					"wallet":         wallet.Hex(),
					"network":        network.GetName(),
					"chain_id":       network.GetChainID(),
					"mined_nonce":    minedNonce,
					"remote_pending": remotePendingNonce,
					"local_pending":  localPendingNonce,
				}).Debug("acquireNonce: abnormal state - mined > remote pending")
				return nil, fmt.Errorf(
					"mined nonce is higher than pending nonce, this is abnormal data from nodes, retry again later",
				)
			}
			// minedNonce == remotePendingNonce (no pending txs on nodes)
			// Use max of local and mined nonce
			if localPendingNonce > minedNonce {
				nextNonce = localPendingNonce
				decisionReason = "no pending on nodes, using local (higher than mined)"
			} else {
				nextNonce = minedNonce
				decisionReason = "no pending on nodes, using mined (>= local)"
			}
		} else {
			// There are pending txs on nodes
			// Use max of local, remote pending nonce
			if localPendingNonce > remotePendingNonce {
				nextNonce = localPendingNonce
				decisionReason = "pending on nodes, using local (higher than remote)"
			} else {
				nextNonce = remotePendingNonce
				decisionReason = "pending on nodes, using remote (>= local)"
			}
		}
	}

	// Reserve this nonce by updating local pending nonce
	// This ensures the next call to acquireNonce will get nextNonce+1
	wm.setPendingNonceUnlocked(wallet, network, nextNonce)

	var localNonceStr string
	if localPendingNonceBig != nil {
		localNonceStr = localPendingNonceBig.String()
	} else {
		localNonceStr = "nil"
	}

	logger.WithFields(logger.Fields{
		"wallet":         wallet.Hex(),
		"network":        network.GetName(),
		"chain_id":       network.GetChainID(),
		"acquired_nonce": nextNonce,
		"mined_nonce":    minedNonce,
		"remote_pending": remotePendingNonce,
		"local_pending":  localNonceStr,
		"decision":       decisionReason,
	}).Debug("acquireNonce: nonce acquired and reserved")

	return big.NewInt(int64(nextNonce)), nil
}

// ReleaseNonce releases a previously acquired nonce that was not used.
// This allows the nonce to be reused by subsequent transactions.
// Note: This only affects local tracking. If the transaction was already broadcast
// to some nodes, calling this may cause issues.
func (wm *WalletManager) ReleaseNonce(wallet common.Address, network networks.Network, nonce uint64) {
	lock := wm.getWalletLock(wallet)
	lock.Lock()
	defer lock.Unlock()

	noncesRaw, ok := wm.pendingNonces.Load(wallet)
	if !ok {
		logger.WithFields(logger.Fields{
			"wallet":   wallet.Hex(),
			"network":  network.GetName(),
			"chain_id": network.GetChainID(),
			"nonce":    nonce,
		}).Debug("ReleaseNonce: no nonce map found for wallet, nothing to release")
		return
	}

	walletNonces := noncesRaw.(map[uint64]*big.Int)
	currentNonce := walletNonces[network.GetChainID()]

	// Only release if this is the most recent nonce
	// (we can only release the "tip" of the nonce sequence)
	if currentNonce != nil && currentNonce.Uint64() == nonce {
		if nonce > 0 {
			walletNonces[network.GetChainID()] = big.NewInt(int64(nonce - 1))
			logger.WithFields(logger.Fields{
				"wallet":         wallet.Hex(),
				"network":        network.GetName(),
				"chain_id":       network.GetChainID(),
				"released_nonce": nonce,
				"new_stored":     nonce - 1,
			}).Debug("ReleaseNonce: nonce released successfully")
		} else {
			delete(walletNonces, network.GetChainID())
			logger.WithFields(logger.Fields{
				"wallet":         wallet.Hex(),
				"network":        network.GetName(),
				"chain_id":       network.GetChainID(),
				"released_nonce": nonce,
			}).Debug("ReleaseNonce: nonce 0 released, removed network entry")
		}
	} else {
		var currentNonceStr string
		if currentNonce != nil {
			currentNonceStr = currentNonce.String()
		} else {
			currentNonceStr = "nil"
		}
		logger.WithFields(logger.Fields{
			"wallet":          wallet.Hex(),
			"network":         network.GetName(),
			"chain_id":        network.GetChainID(),
			"requested_nonce": nonce,
			"current_nonce":   currentNonceStr,
		}).Debug("ReleaseNonce: skipped - not the tip nonce")
	}
}

// nonce is deprecated: use acquireNonce instead for race-safe nonce acquisition.
// This function is kept for backward compatibility but has race conditions
// when called concurrently for the same wallet/network.
func (wm *WalletManager) nonce(wallet common.Address, network networks.Network) (*big.Int, error) {
	return wm.acquireNonce(wallet, network)
}

func (wm *WalletManager) getGasSettingInfo(network networks.Network) *GasInfo {
	if info, ok := wm.gasSettings.Load(network.GetChainID()); ok {
		return info.(*GasInfo)
	}
	return nil
}

func (wm *WalletManager) setGasInfo(network networks.Network, info *GasInfo) {
	wm.gasSettings.Store(network.GetChainID(), info)
}

// GasSetting returns cached gas settings for the network, refreshing if stale.
func (wm *WalletManager) GasSetting(network networks.Network) (*GasInfo, error) {
	gasInfo := wm.getGasSettingInfo(network)
	if gasInfo == nil || time.Since(gasInfo.Timestamp) >= GAS_INFO_TTL {
		// gasInfo is not initiated or outdated
		r, err := wm.Reader(network)
		if err != nil {
			return nil, fmt.Errorf("couldn't get reader for gas settings: %w", err)
		}
		gasPrice, gasTipCapGwei, err := r.SuggestedGasSettings()
		if err != nil {
			return nil, fmt.Errorf("couldn't get gas settings in context manager: %w", err)
		}

		info := GasInfo{
			GasPrice:         gasPrice,
			BaseGasPrice:     nil,
			MaxPriorityPrice: gasTipCapGwei,
			FeePerGas:        gasPrice,
			Timestamp:        time.Now(),
		}
		wm.setGasInfo(network, &info)
		return &info, nil
	}
	return wm.getGasSettingInfo(network), nil
}

func (wm *WalletManager) BuildTx(
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
) (tx *types.Transaction, err error) {
	// Track whether we acquired the nonce (for cleanup on failure)
	var acquiredNonce bool
	var nonceValue uint64

	// Cleanup function to release nonce if we fail after acquiring it
	defer func() {
		if err != nil && acquiredNonce {
			wm.ReleaseNonce(from, network, nonceValue)
		}
	}()

	if gasLimit == 0 {
		r, readerErr := wm.Reader(network)
		if readerErr != nil {
			return nil, errors.Join(ErrEstimateGasFailed, fmt.Errorf("couldn't get reader: %w", readerErr))
		}
		gasLimit, err = r.EstimateExactGas(
			from.Hex(), to.Hex(),
			gasPrice,
			value,
			data,
		)

		if err != nil {
			return nil, errors.Join(ErrEstimateGasFailed, fmt.Errorf("couldn't estimate gas. The tx is meant to revert or network error. Detail: %w", err))
		}
	}

	if nonce == nil {
		nonce, err = wm.nonce(from, network)
		if err != nil {
			return nil, errors.Join(ErrAcquireNonceFailed, fmt.Errorf("couldn't get nonce of the wallet from any nodes: %w", err))
		}
		acquiredNonce = true
		nonceValue = nonce.Uint64()
	}

	if gasPrice == 0 {
		gasInfo, gasErr := wm.GasSetting(network)
		if gasErr != nil {
			err = errors.Join(ErrGetGasSettingFailed, fmt.Errorf("couldn't get gas price info from any nodes: %w", gasErr))
			return nil, err
		}
		gasPrice = gasInfo.GasPrice
		tipCapGwei = gasInfo.MaxPriorityPrice
	}

	gasPriceToUse := gasPrice + extraGasPrice
	tipCapGweiToUse := tipCapGwei + extraTipCapGwei
	if tipCapGweiToUse > gasPriceToUse {
		gasPriceToUse = tipCapGweiToUse
	}

	// Success - clear acquiredNonce so defer doesn't release it
	acquiredNonce = false

	return jarviscommon.BuildExactTx(
		txType,
		nonce.Uint64(),
		to.Hex(),
		value,
		gasLimit+extraGasLimit,
		gasPriceToUse,
		tipCapGweiToUse,
		data,
		network.GetChainID(),
	), nil
}

func (wm *WalletManager) SignTx(
	wallet common.Address,
	tx *types.Transaction,
	network networks.Network,
) (signedAddr common.Address, signedTx *types.Transaction, err error) {
	acc := wm.Account(wallet)
	if acc == nil {
		acc, err = wm.UnlockAccount(wallet)
		if err != nil {
			return common.Address{}, nil, fmt.Errorf(
				"the wallet to sign txs is not registered in context manager",
			)
		}
	}
	return acc.SignTx(tx, big.NewInt(int64(network.GetChainID())))
}

func (wm *WalletManager) registerBroadcastedTx(tx *types.Transaction, network networks.Network) error {
	wallet, err := jarviscommon.GetSignerAddressFromTx(tx, big.NewInt(int64(network.GetChainID())))
	if err != nil {
		return fmt.Errorf("couldn't derive sender from the tx data in context manager: %s", err)
	}
	// update nonce
	wm.setPendingNonce(wallet, network, tx.Nonce())
	// update txs
	wm.setTx(wallet, network, tx)
	return nil
}

func (wm *WalletManager) BroadcastTx(
	tx *types.Transaction,
) (hash string, broadcasted bool, err BroadcastError) {
	network, networkErr := networks.GetNetworkByID(tx.ChainId().Uint64())
	// TODO: handle chainId 0 for old txs
	if networkErr != nil {
		return "", false, BroadcastError(fmt.Errorf("tx is encoded with unsupported ChainID: %w", networkErr))
	}
	b, broadcasterErr := wm.Broadcaster(network)
	if broadcasterErr != nil {
		return "", false, BroadcastError(fmt.Errorf("couldn't get broadcaster: %w", broadcasterErr))
	}
	hash, broadcasted, allErrors := b.BroadcastTx(tx)
	if broadcasted {
		regErr := wm.registerBroadcastedTx(tx, network)
		if regErr != nil {
			return "", false, BroadcastError(fmt.Errorf("couldn't register broadcasted tx in context manager: %w", regErr))
		}
	}
	return hash, broadcasted, NewBroadcastError(allErrors)
}

func (wm *WalletManager) BroadcastTxSync(
	tx *types.Transaction,
) (receipt *types.Receipt, err error) {
	network, networkErr := networks.GetNetworkByID(tx.ChainId().Uint64())
	if networkErr != nil {
		return nil, fmt.Errorf("tx is encoded with unsupported ChainID: %w", networkErr)
	}
	b, broadcasterErr := wm.Broadcaster(network)
	if broadcasterErr != nil {
		return nil, NewBroadcastError(fmt.Errorf("couldn't get broadcaster: %w", broadcasterErr))
	}
	receipt, err = b.BroadcastTxSync(tx)
	if err != nil {
		return nil, NewBroadcastError(fmt.Errorf("couldn't broadcast sync tx: %w", err))
	}
	err = wm.registerBroadcastedTx(tx, network)
	if err != nil {
		return nil, NewBroadcastError(fmt.Errorf("couldn't register broadcasted tx in context manager: %w", err))
	}
	return receipt, nil
}

// createErrorDecoder creates an error decoder from ABIs if available
func (wm *WalletManager) createErrorDecoder(abis []abi.ABI) *ErrorDecoder {
	if len(abis) == 0 {
		return nil
	}

	errDecoder, err := NewErrorDecoder(abis...)
	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Error("Failed to create error decoder. Ignore and continue")
		return nil
	}
	return errDecoder
}

type TxStatus struct {
	Status  string
	Receipt *types.Receipt
}

// MonitorTx non-blocking way to monitor the tx status, it returns a channel that will be closed when the tx monitoring is done
// the channel is supposed to receive the following values:
//  1. "mined" if the tx is mined
//  2. "slow" if the tx is too slow to be mined (so receiver might want to retry with higher gas price)
//  3. other strings if the tx failed and the reason is returned by the node or other debugging error message that the node can return
//
// Deprecated: Use MonitorTxContext instead for better cancellation support.
func (wm *WalletManager) MonitorTx(tx *types.Transaction, network networks.Network, txCheckInterval time.Duration) <-chan TxStatus {
	return wm.MonitorTxContext(context.Background(), tx, network, txCheckInterval)
}

// MonitorTxContext is a context-aware version of MonitorTx that supports cancellation.
// When the context is cancelled, the monitoring goroutine will exit and close the channel.
func (wm *WalletManager) MonitorTxContext(ctx context.Context, tx *types.Transaction, network networks.Network, txCheckInterval time.Duration) <-chan TxStatus {
	txMonitor := wm.getTxMonitor(network)
	statusChan := make(chan TxStatus, 1) // Buffered to avoid goroutine leak on context cancellation
	monitorChan := txMonitor.MakeWaitChannelWithInterval(tx.Hash().Hex(), txCheckInterval)
	go func() {
		defer close(statusChan)
		select {
		case <-ctx.Done():
			statusChan <- TxStatus{
				Status:  "cancelled",
				Receipt: nil,
			}
		case status := <-monitorChan:
			switch status.Status {
			case "done":
				statusChan <- TxStatus{
					Status:  "mined",
					Receipt: status.Receipt,
				}
			case "reverted":
				statusChan <- TxStatus{
					Status:  "reverted",
					Receipt: status.Receipt,
				}
			case "lost":
				statusChan <- TxStatus{
					Status:  "lost",
					Receipt: nil,
				}
			default:
				// ignore other statuses
			}
		case <-time.After(5 * time.Second):
			statusChan <- TxStatus{
				Status:  "slow",
				Receipt: nil,
			}
		}
	}()
	return statusChan
}

func (wm *WalletManager) getTxStatuses(oldTxs map[string]*types.Transaction, network networks.Network) (statuses map[string]jarviscommon.TxInfo, err error) {
	result := map[string]jarviscommon.TxInfo{}

	r, readerErr := wm.Reader(network)
	if readerErr != nil {
		return nil, fmt.Errorf("couldn't get reader for tx statuses: %w", readerErr)
	}

	for _, tx := range oldTxs {
		txInfo, _ := r.TxInfoFromHash(tx.Hash().Hex())
		result[tx.Hash().Hex()] = txInfo
	}

	return result, nil
}

// EnsureTxWithHooks ensures the tx is broadcasted and mined, it will retry until the tx is mined.
// This is a convenience wrapper that uses context.Background().
// For production use, prefer EnsureTxWithHooksContext to allow cancellation.
func (wm *WalletManager) EnsureTxWithHooks(
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
) (tx *types.Transaction, receipt *types.Receipt, err error) {
	return wm.EnsureTxWithHooksContext(
		context.Background(),
		numRetries,
		sleepDuration,
		txCheckInterval,
		txType,
		from,
		to,
		value,
		gasLimit, extraGasLimit,
		gasPrice, extraGasPrice,
		tipCapGwei, extraTipCapGwei,
		maxGasPrice, maxTipCap,
		data,
		network,
		beforeSignAndBroadcastHook,
		afterSignAndBroadcastHook,
		abis,
		gasEstimationFailedHook,
		nil, // simulationFailedHook
		nil, // txMinedHook
	)
}

// EnsureTxWithHooksContext ensures the tx is broadcasted and mined, it will retry until the tx is mined.
// The context allows the caller to cancel the operation at any point.
//
// It returns nil and error if:
//  1. the tx couldn't be built
//  2. the tx couldn't be broadcasted and get mined after numRetries retries
//  3. the context is cancelled
//
// It always returns the tx that was mined, either if the tx was successful or reverted.
//
// Possible errors:
//  1. ErrEstimateGasFailed
//  2. ErrAcquireNonceFailed
//  3. ErrGetGasSettingFailed
//  4. ErrEnsureTxOutOfRetries
//  5. ErrGasPriceLimitReached
//  6. context.Canceled or context.DeadlineExceeded
//
// # If the caller wants to know the reason of the error, they can use errors.Is to check if the error is one of the above
//
// After building the tx and before signing and broadcasting, the caller can provide a function hook to receive the tx and building error,
// if the hook returns an error, the process will be stopped and the error will be returned. If the hook returns nil, the process will continue
// even if the tx building failed, in this case, it will retry with the same data up to numRetries times and the hook will be called again.
//
// After signing and broadcasting successfully, the caller can provide a function hook to receive the signed tx and broadcast error,
// if the hook returns an error, the process will be stopped and the error will be returned. If the hook returns nil, the process will continue
// to monitor the tx to see if the tx is mined or not. If the tx is not mined, the process will retry either with a new nonce or with higher gas
// price and tip cap to ensure the tx is mined. Hooks will be called again in the retry process.
func (wm *WalletManager) EnsureTxWithHooksContext(
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
) (tx *types.Transaction, receipt *types.Receipt, err error) {
	// Create execution context
	execCtx, err := NewTxExecutionContext(
		numRetries, sleepDuration, txCheckInterval,
		txType, from, to, value,
		gasLimit, extraGasLimit,
		gasPrice, extraGasPrice,
		tipCapGwei, extraTipCapGwei,
		maxGasPrice, maxTipCap,
		data, network,
		beforeSignAndBroadcastHook, afterSignAndBroadcastHook,
		abis, gasEstimationFailedHook,
		simulationFailedHook, txMinedHook,
	)
	if err != nil {
		return nil, nil, err
	}

	// Cleanup function to release nonce if we exit with an error and no tx was broadcast
	defer func() {
		if err != nil && len(execCtx.oldTxs) == 0 && execCtx.retryNonce != nil {
			// We have a reserved nonce but no tx was ever broadcast - release it
			wm.ReleaseNonce(from, network, execCtx.retryNonce.Uint64())
		}
	}()

	// Create error decoder
	errDecoder := wm.createErrorDecoder(abis)

	// Main execution loop
	for {
		// Check for context cancellation before each iteration
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return nil, nil, err
		default:
		}

		// Only sleep after actual retry attempts, not slow monitoring
		if execCtx.actualRetryCount > 0 {
			// Use a timer so we can also check for context cancellation during sleep
			sleepTimer := time.NewTimer(execCtx.sleepDuration)
			select {
			case <-ctx.Done():
				sleepTimer.Stop()
				err = ctx.Err()
				return nil, nil, err
			case <-sleepTimer.C:
			}
		}

		// Execute transaction attempt
		result := wm.executeTransactionAttempt(ctx, execCtx, errDecoder)

		if result.ShouldReturn {
			err = result.Error
			return result.Transaction, result.Receipt, err
		}
		if result.ShouldRetry {
			continue
		}

		// Monitor and handle the transaction (only if we have a transaction to monitor)
		// in this case, result.Receipt can be filled already because of this rpc https://www.quicknode.com/docs/arbitrum/eth_sendRawTransactionSync
		if result.Transaction != nil && result.Receipt == nil {
			statusChan := wm.MonitorTxContext(ctx, result.Transaction, execCtx.network, execCtx.txCheckInterval)

			// Wait for status from the context-aware monitor
			status := <-statusChan
			if status.Status == "cancelled" {
				err = ctx.Err()
				return nil, nil, err
			}
			result = wm.handleTransactionStatus(status, result.Transaction, execCtx)
			if result.ShouldReturn {
				err = result.Error
				return result.Transaction, result.Receipt, err
			}
			if result.ShouldRetry {
				continue
			}
		}
	}
}

// executeTransactionAttempt handles building and broadcasting a single transaction attempt
func (wm *WalletManager) executeTransactionAttempt(ctx context.Context, execCtx *TxExecutionContext, errDecoder *ErrorDecoder) *TxExecutionResult {
	// Build transaction
	builtTx, err := wm.BuildTx(
		execCtx.txType,
		execCtx.from,
		execCtx.to,
		execCtx.retryNonce,
		execCtx.value,
		execCtx.gasLimit,
		execCtx.extraGasLimit,
		execCtx.retryGasPrice,
		execCtx.extraGasPrice,
		execCtx.retryTipCap,
		execCtx.extraTipCapGwei,
		execCtx.data,
		execCtx.network,
	)

	// Handle gas estimation failure
	if errors.Is(err, ErrEstimateGasFailed) {
		return wm.handleGasEstimationFailure(execCtx, errDecoder, err)
	}

	// If builtTx is nil, skip this iteration
	if builtTx == nil {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  true,
			ShouldReturn: false,
			Error:        nil,
		}
	}

	// simulate the tx at pending state to see if it will be reverted
	r, readerErr := wm.Reader(execCtx.network)
	if readerErr != nil {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        errors.Join(ErrSimulatedTxFailed, fmt.Errorf("couldn't get reader for simulation: %w", readerErr)),
		}
	}
	_, err = r.EthCall(execCtx.from.Hex(), execCtx.to.Hex(), execCtx.data, nil)
	if err != nil {
		revertData, isRevert := ethclient.RevertErrorData(err)
		if isRevert {
			return wm.handleEthCallRevertFailure(execCtx, errDecoder, builtTx, revertData, err)
		} else {
			err = errors.Join(ErrSimulatedTxFailed, fmt.Errorf("couldn't simulate tx at pending state. Detail: %w", err))
			logger.WithFields(logger.Fields{
				"tx_hash":         builtTx.Hash().Hex(),
				"nonce":           builtTx.Nonce(),
				"gas_price":       builtTx.GasPrice().String(),
				"tip_cap":         builtTx.GasTipCap().String(),
				"max_fee_per_gas": builtTx.GasFeeCap().String(),
				"used_sync_tx":    execCtx.network.IsSyncTxSupported(),
				"error":           err,
			}).Debug("Tx simulation failed but not a revert error")
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        err,
			}
		}
	}

	// Execute hooks and broadcast
	result := wm.signAndBroadcastTransaction(builtTx, execCtx)

	// If no transaction is set in result, use the built transaction
	if result.Transaction == nil && !result.ShouldReturn {
		result.Transaction = builtTx
	}

	return result
}

func (wm *WalletManager) handleEthCallRevertFailure(execCtx *TxExecutionContext, errDecoder *ErrorDecoder, builtTx *types.Transaction, revertData []byte, err error) *TxExecutionResult {
	var abiError *abi.Error
	var revertParams any

	if errDecoder != nil {
		abiError, revertParams, _ = errDecoder.Decode(err)
		err = errors.Join(ErrSimulatedTxReverted, fmt.Errorf("revert error: %s. revert params: %+v. Detail: %w", abiError.Name, revertParams, err))
	} else {
		err = errors.Join(ErrSimulatedTxReverted, fmt.Errorf("revert data: %s. Detail: %w", common.Bytes2Hex(revertData), err))
	}

	logger.WithFields(logger.Fields{
		"tx_hash":         builtTx.Hash().Hex(),
		"nonce":           builtTx.Nonce(),
		"gas_price":       builtTx.GasPrice().String(),
		"tip_cap":         builtTx.GasTipCap().String(),
		"max_fee_per_gas": builtTx.GasFeeCap().String(),
		"used_sync_tx":    execCtx.network.IsSyncTxSupported(),
		"error":           err,
		"revert_data":     revertData,
	}).Debug("Tx simulation showed a revert error")

	// Call simulation failed hook if set
	if execCtx.simulationFailedHook != nil {
		shouldRetry, hookErr := execCtx.simulationFailedHook(builtTx, revertData, abiError, revertParams, err)
		if hookErr != nil {
			// Release nonce since we're giving up and tx was never broadcast
			wm.ReleaseNonce(execCtx.from, execCtx.network, builtTx.Nonce())
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        fmt.Errorf("simulation failed hook error: %w", hookErr),
			}
		}
		if !shouldRetry {
			// Release nonce since we're giving up and tx was never broadcast
			wm.ReleaseNonce(execCtx.from, execCtx.network, builtTx.Nonce())
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        err,
			}
		}
	}

	// we need to persist a few calculated values here before retrying with the txs
	execCtx.retryNonce = big.NewInt(int64(builtTx.Nonce()))
	execCtx.gasLimit = builtTx.Gas()
	// we could persist the error here but then later we need to set it to nil, setting this to the error if the user is supposed to handle such error
	return &TxExecutionResult{
		Transaction:  nil,
		ShouldRetry:  true,
		ShouldReturn: false,
		Error:        nil,
	}
}

// handleGasEstimationFailure processes gas estimation failures and pending transaction checks
func (wm *WalletManager) handleGasEstimationFailure(execCtx *TxExecutionContext, errDecoder *ErrorDecoder, err error) *TxExecutionResult {
	// Only check old transactions if we have any
	if len(execCtx.oldTxs) > 0 {
		// Check if previous transactions might have been successful
		statuses, statusErr := wm.getTxStatuses(execCtx.oldTxs, execCtx.network)
		if statusErr != nil {
			logger.WithFields(logger.Fields{
				"error": statusErr,
			}).Debug("Getting tx statuses after gas estimation failure. Ignore and continue the retry loop")
			// Don't return immediately, proceed to hook handling
		} else {
			// Check for completed transactions
			for txhash, status := range statuses {
				if status.Status == "done" || status.Status == "reverted" {
					if tx, exists := execCtx.oldTxs[txhash]; exists && tx != nil {
						return &TxExecutionResult{
							Transaction:  tx,
							ShouldRetry:  false,
							ShouldReturn: true,
							Error:        nil,
						}
					}
				}
			}

			// Find highest gas price pending transaction to monitor
			highestGasPrice := big.NewInt(0)
			var bestTx *types.Transaction
			for txhash, status := range statuses {
				if status.Status == "pending" {
					if tx, exists := execCtx.oldTxs[txhash]; exists && tx != nil {
						if tx.GasPrice().Cmp(highestGasPrice) > 0 {
							highestGasPrice = tx.GasPrice()
							bestTx = tx
						}
					}
				}
			}

			if bestTx != nil {
				// We have a pending transaction, monitor it instead of retrying
				logger.WithFields(logger.Fields{
					"tx_hash": bestTx.Hash().Hex(),
					"nonce":   bestTx.Nonce(),
				}).Info("Found pending transaction during gas estimation failure, monitoring it instead")
				// Return the transaction but don't set ShouldReturn so it goes to monitoring
				return &TxExecutionResult{
					Transaction:  bestTx,
					ShouldRetry:  false,
					ShouldReturn: false,
					Error:        nil,
				}
			}
		}
	}

	// Handle gas estimation failed hook
	if errDecoder != nil && execCtx.gasEstimationFailedHook != nil {
		abiError, revertParams, revertMsgErr := errDecoder.Decode(err)
		hookGasLimit, hookErr := execCtx.gasEstimationFailedHook(nil, abiError, revertParams, revertMsgErr, err)
		if hookErr != nil {
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        hookErr,
			}
		}
		if hookGasLimit != nil {
			execCtx.gasLimit = hookGasLimit.Uint64()
		}
	}

	// Increment retry count for gas estimation failure
	execCtx.actualRetryCount++
	if execCtx.actualRetryCount > execCtx.numRetries {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        errors.Join(ErrEnsureTxOutOfRetries, fmt.Errorf("gas estimation failed after %d retries", execCtx.numRetries)),
		}
	}

	return &TxExecutionResult{
		Transaction:  nil,
		ShouldRetry:  true,
		ShouldReturn: false,
		Error:        nil,
	}
}

// signAndBroadcastTransaction handles the signing and broadcasting process
func (wm *WalletManager) signAndBroadcastTransaction(tx *types.Transaction, execCtx *TxExecutionContext) *TxExecutionResult {
	// Helper to release nonce when we fail before broadcast attempt
	releaseNonce := func() {
		// Only release if this tx is not in oldTxs (meaning it was never broadcast)
		if _, exists := execCtx.oldTxs[tx.Hash().Hex()]; !exists {
			wm.ReleaseNonce(execCtx.from, execCtx.network, tx.Nonce())
		}
	}

	// Execute before hook
	if execCtx.beforeSignAndBroadcastHook != nil {
		if hookError := execCtx.beforeSignAndBroadcastHook(tx, nil); hookError != nil {
			releaseNonce()
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        fmt.Errorf("after tx building and before signing and broadcasting hook error: %w", hookError),
			}
		}
	}

	// Sign transaction
	signedAddr, signedTx, err := wm.SignTx(execCtx.from, tx, execCtx.network)
	if err != nil {
		releaseNonce()
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        fmt.Errorf("failed to sign transaction: %w", err),
		}
	}

	// Verify signed address matches expected address
	if signedAddr.Cmp(execCtx.from) != 0 {
		releaseNonce()
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error: fmt.Errorf(
				"signed from wrong address. You could use wrong hw or passphrase. Expected wallet: %s, signed wallet: %s",
				execCtx.from.Hex(),
				signedAddr.Hex(),
			),
		}
	}

	var receipt *types.Receipt
	var broadcastErr BroadcastError
	var successful bool

	// Broadcast transaction
	if execCtx.network.IsSyncTxSupported() {
		receipt, broadcastErr = wm.BroadcastTxSync(signedTx)
		if receipt != nil {
			successful = true
		}
	} else {
		_, successful, broadcastErr = wm.BroadcastTx(signedTx)
	}

	if signedTx != nil {
		execCtx.oldTxs[signedTx.Hash().Hex()] = signedTx
	}

	if !successful {
		// Only log if signedTx is not nil to avoid panic
		if signedTx != nil {
			logger.WithFields(logger.Fields{
				"tx_hash":         signedTx.Hash().Hex(),
				"nonce":           signedTx.Nonce(),
				"gas_price":       signedTx.GasPrice().String(),
				"tip_cap":         signedTx.GasTipCap().String(),
				"max_fee_per_gas": signedTx.GasFeeCap().String(),
				"used_sync_tx":    execCtx.network.IsSyncTxSupported(),
				"receipt":         receipt,
				"error":           broadcastErr,
			}).Debug("Unsuccessful signing and broadcasting transaction")
		} else {
			logger.WithFields(logger.Fields{
				"nonce":        tx.Nonce(),
				"gas_price":    tx.GasPrice().String(),
				"used_sync_tx": execCtx.network.IsSyncTxSupported(),
				"receipt":      receipt,
				"error":        broadcastErr,
			}).Debug("Unsuccessful signing and broadcasting transaction (no signed tx)")
		}

		return wm.handleBroadcastError(broadcastErr, tx, execCtx)
	}

	// Log successful broadcast
	logger.WithFields(logger.Fields{
		"tx_hash":         signedTx.Hash().Hex(),
		"nonce":           signedTx.Nonce(),
		"gas_price":       signedTx.GasPrice().String(),
		"tip_cap":         signedTx.GasTipCap().String(),
		"max_fee_per_gas": signedTx.GasFeeCap().String(),
		"used_sync_tx":    execCtx.network.IsSyncTxSupported(),
		"receipt":         receipt,
	}).Info("Signed and broadcasted transaction")

	// Execute after hook - convert BroadcastError to error for hook
	var hookErr error
	if broadcastErr != nil {
		hookErr = broadcastErr
	}

	if execCtx.afterSignAndBroadcastHook != nil {
		if hookError := execCtx.afterSignAndBroadcastHook(signedTx, hookErr); hookError != nil {
			return &TxExecutionResult{
				Transaction:  signedTx,
				Receipt:      receipt,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        fmt.Errorf("after signing and broadcasting hook error: %w", hookError),
			}
		}
	}

	// in case receipt is not nil, it means the tx is broadcasted and mined using eth_sendRawTransactionSync
	if receipt != nil {
		// Call txMinedHook if set
		if execCtx.txMinedHook != nil {
			if hookErr := execCtx.txMinedHook(signedTx, receipt); hookErr != nil {
				return &TxExecutionResult{
					Transaction:  signedTx,
					Receipt:      receipt,
					ShouldRetry:  false,
					ShouldReturn: true,
					Error:        fmt.Errorf("tx mined hook error: %w", hookErr),
				}
			}
		}
		return &TxExecutionResult{
			Transaction:  signedTx,
			Receipt:      receipt,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        nil,
		}
	}

	return &TxExecutionResult{
		Transaction:  signedTx,
		ShouldRetry:  false,
		ShouldReturn: false,
		Receipt:      receipt,
		Error:        nil,
	}
}

// handleBroadcastError processes various broadcast errors and determines retry strategy
func (wm *WalletManager) handleBroadcastError(broadcastErr BroadcastError, tx *types.Transaction, execCtx *TxExecutionContext) *TxExecutionResult {
	// Special case: nonce is low requires checking if transaction is already mined
	if broadcastErr == ErrNonceIsLow {
		return wm.handleNonceIsLowError(tx, execCtx)
	}

	// Special case: tx is known doesn't count as retry (we're just waiting for it to be mined)
	// in this case, we need to speed up the tx by increasing the gas price and tip cap
	// however, it should be handled by the slow status gotten from the monitor tx
	// so we just need to retry with the same nonce
	if broadcastErr == ErrTxIsKnown {
		execCtx.retryNonce = big.NewInt(int64(tx.Nonce()))
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  true,
			ShouldReturn: false,
			Error:        nil,
		}
	}

	// All other errors count as retry attempts
	var errorMsg string
	switch broadcastErr {
	case ErrInsufficientFund:
		errorMsg = "insufficient fund"
	case ErrGasLimitIsTooLow:
		errorMsg = "gas limit too low"
	default:
		errorMsg = fmt.Sprintf("broadcast error: %v", broadcastErr)
	}

	if result := execCtx.incrementRetryCountAndCheck(errorMsg); result != nil {
		return result
	}

	// Keep the same nonce for retry
	execCtx.retryNonce = big.NewInt(int64(tx.Nonce()))
	return &TxExecutionResult{
		Transaction:  nil,
		ShouldRetry:  true,
		ShouldReturn: false,
		Error:        nil,
	}
}

// handleNonceIsLowError specifically handles the nonce is low error case
func (wm *WalletManager) handleNonceIsLowError(tx *types.Transaction, execCtx *TxExecutionContext) *TxExecutionResult {

	statuses, err := wm.getTxStatuses(execCtx.oldTxs, execCtx.network)

	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Debug("Getting tx statuses in case where tx wasn't broadcasted because nonce is too low. Ignore and continue the retry loop")

		if result := execCtx.incrementRetryCountAndCheck("nonce is low and status check failed"); result != nil {
			return result
		}

		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  true,
			ShouldReturn: false,
			Error:        nil,
		}
	}

	// Check if any old transaction is completed
	for txhash, status := range statuses {
		if status.Status == "done" || status.Status == "reverted" {
			if tx, exists := execCtx.oldTxs[txhash]; exists && tx != nil {
				return &TxExecutionResult{
					Transaction:  tx,
					ShouldRetry:  false,
					ShouldReturn: true,
					Error:        nil,
				}
			}
		}
	}

	// No completed transactions found, retry with new nonce
	if result := execCtx.incrementRetryCountAndCheck("nonce is low and no pending transactions"); result != nil {
		return result
	}

	execCtx.retryNonce = nil

	return &TxExecutionResult{
		Transaction:  nil,
		ShouldRetry:  true,
		ShouldReturn: false,
		Error:        nil,
	}
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

// handleTransactionStatus processes different transaction statuses
func (wm *WalletManager) handleTransactionStatus(status TxStatus, signedTx *types.Transaction, execCtx *TxExecutionContext) *TxExecutionResult {
	switch status.Status {
	case "mined", "reverted":
		// Call txMinedHook if set
		if execCtx.txMinedHook != nil {
			if hookErr := execCtx.txMinedHook(signedTx, status.Receipt); hookErr != nil {
				return &TxExecutionResult{
					Transaction:  signedTx,
					Receipt:      status.Receipt,
					ShouldRetry:  false,
					ShouldReturn: true,
					Error:        fmt.Errorf("tx mined hook error: %w", hookErr),
				}
			}
		}
		return &TxExecutionResult{
			Transaction:  signedTx,
			Receipt:      status.Receipt,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        nil,
		}

	case "lost":
		logger.WithFields(logger.Fields{
			"tx_hash": signedTx.Hash().Hex(),
		}).Info("Transaction lost, retrying...")

		execCtx.actualRetryCount++
		if execCtx.actualRetryCount > execCtx.numRetries {
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        errors.Join(ErrEnsureTxOutOfRetries, fmt.Errorf("transaction lost after %d retries", execCtx.numRetries)),
			}
		}
		execCtx.retryNonce = nil

		// Sleep will be handled in main loop based on actualRetryCount
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  true,
			ShouldReturn: false,
			Error:        nil,
		}

	case "slow":
		// Try to adjust gas prices for slow transaction
		if execCtx.adjustGasPricesForSlowTx(signedTx) {
			logger.WithFields(logger.Fields{
				"tx_hash": signedTx.Hash().Hex(),
			}).Info(fmt.Sprintf("Transaction slow, continuing to monitor with increased gas price by %.0f%% and tip cap by %.0f%%...",
				(GasPriceIncreasePercent-1)*100, (TipCapIncreasePercent-1)*100))

			// Continue retrying with adjusted gas prices
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  true,
				ShouldReturn: false,
				Error:        nil,
			}
		} else {
			// Limits reached - stop retrying and return error
			logger.WithFields(logger.Fields{
				"tx_hash":       signedTx.Hash().Hex(),
				"max_gas_price": execCtx.maxGasPrice,
				"max_tip_cap":   execCtx.maxTipCap,
			}).Warn("Transaction slow but gas price protection limits reached. Stopping retry attempts.")

			return &TxExecutionResult{
				Transaction:  signedTx,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        errors.Join(ErrGasPriceLimitReached, fmt.Errorf("maxGasPrice: %f, maxTipCap: %f", execCtx.maxGasPrice, execCtx.maxTipCap)),
			}
		}

	default:
		// Unknown status, treat as retry
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  true,
			ShouldReturn: false,
			Error:        nil,
		}
	}
}

func (wm *WalletManager) EnsureTx(
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
) (tx *types.Transaction, receipt *types.Receipt, err error) {
	return wm.EnsureTxWithHooks(
		DefaultNumRetries,
		DefaultSleepDuration,
		DefaultTxCheckInterval,
		txType,
		from,
		to,
		value,
		gasLimit, extraGasLimit,
		gasPrice, extraGasPrice,
		tipCapGwei, extraTipCapGwei,
		0, 0, // Default maxGasPrice and maxTipCap (0 means no limit)
		data,
		network,
		nil,
		nil,
		nil,
		nil,
	)
}
