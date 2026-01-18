package walletarmy

import (
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/txanalyzer"
	"github.com/tranvictor/jarvis/util"
	"github.com/tranvictor/jarvis/util/broadcaster"
	"github.com/tranvictor/jarvis/util/monitor"
	"github.com/tranvictor/jarvis/util/reader"

	"github.com/tranvictor/walletarmy/internal/circuitbreaker"
)

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

// GetCircuitBreakerStats returns the circuit breaker statistics for a network
func (wm *WalletManager) GetCircuitBreakerStats(network networks.Network) circuitbreaker.Stats {
	return wm.getCircuitBreaker(network.GetChainID()).Stats()
}

// ResetCircuitBreaker resets the circuit breaker for a network
func (wm *WalletManager) ResetCircuitBreaker(network networks.Network) {
	wm.getCircuitBreaker(network.GetChainID()).Reset()
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
