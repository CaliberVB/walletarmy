// adapters.go provides adapter implementations that wrap jarvis types
// to implement the minimal interfaces defined in deps.go.
package walletarmy

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient/gethclient"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/util"
	"github.com/tranvictor/jarvis/util/broadcaster"
	"github.com/tranvictor/jarvis/util/monitor"
	"github.com/tranvictor/jarvis/util/reader"
)

// readerAdapter wraps jarvis reader.EthReader to implement our EthReader interface
type readerAdapter struct {
	reader *reader.EthReader
}

func (r *readerAdapter) GetMinedNonce(addr string) (uint64, error) {
	return r.reader.GetMinedNonce(addr)
}

func (r *readerAdapter) GetPendingNonce(addr string) (uint64, error) {
	return r.reader.GetPendingNonce(addr)
}

func (r *readerAdapter) EstimateExactGas(from, to string, gasPrice float64, value *big.Int, data []byte) (uint64, error) {
	return r.reader.EstimateExactGas(from, to, gasPrice, value, data)
}

func (r *readerAdapter) SuggestedGasSettings() (float64, float64, error) {
	return r.reader.SuggestedGasSettings()
}

func (r *readerAdapter) EthCall(from, to string, data []byte, overrides *map[common.Address]gethclient.OverrideAccount) ([]byte, error) {
	return r.reader.EthCall(from, to, data, overrides)
}

func (r *readerAdapter) TxInfoFromHash(hash string) (TxInfo, error) {
	info, err := r.reader.TxInfoFromHash(hash)
	if err != nil {
		return TxInfo{}, err
	}
	return TxInfo{
		Status:  info.Status,
		Receipt: info.Receipt,
	}, nil
}

// NewReaderAdapter creates an EthReader from a jarvis reader
func NewReaderAdapter(r *reader.EthReader) EthReader {
	return &readerAdapter{reader: r}
}

// broadcasterAdapter wraps jarvis broadcaster.Broadcaster to implement our EthBroadcaster interface
type broadcasterAdapter struct {
	broadcaster *broadcaster.Broadcaster
}

func (b *broadcasterAdapter) BroadcastTx(tx *types.Transaction) (string, bool, error) {
	return b.broadcaster.BroadcastTx(tx)
}

func (b *broadcasterAdapter) BroadcastTxSync(tx *types.Transaction) (*types.Receipt, error) {
	return b.broadcaster.BroadcastTxSync(tx)
}

// NewBroadcasterAdapter creates an EthBroadcaster from a jarvis broadcaster
func NewBroadcasterAdapter(b *broadcaster.Broadcaster) EthBroadcaster {
	return &broadcasterAdapter{broadcaster: b}
}

// txMonitorAdapter wraps jarvis monitor.TxMonitor to implement our TxMonitor interface
type txMonitorAdapter struct {
	monitor *monitor.TxMonitor
}

func (m *txMonitorAdapter) MakeWaitChannelWithInterval(txHash string, interval time.Duration) <-chan TxMonitorStatus {
	jarvisChan := m.monitor.MakeWaitChannelWithInterval(txHash, interval)
	resultChan := make(chan TxMonitorStatus, 1)

	go func() {
		defer close(resultChan)
		status := <-jarvisChan
		resultChan <- TxMonitorStatus{
			Status:  status.Status,
			Receipt: status.Receipt,
		}
	}()

	return resultChan
}

// NewTxMonitorAdapter creates a TxMonitor from a jarvis monitor
func NewTxMonitorAdapter(m *monitor.TxMonitor) TxMonitor {
	return &txMonitorAdapter{monitor: m}
}

// DefaultReaderFactory is the default factory that creates jarvis readers
func DefaultReaderFactory(network networks.Network) (EthReader, error) {
	r, err := util.EthReader(network)
	if err != nil {
		return nil, err
	}
	return NewReaderAdapter(r), nil
}

// DefaultBroadcasterFactory is the default factory that creates jarvis broadcasters
func DefaultBroadcasterFactory(network networks.Network) (EthBroadcaster, error) {
	b, err := util.EthBroadcaster(network)
	if err != nil {
		return nil, err
	}
	return NewBroadcasterAdapter(b), nil
}

// DefaultTxMonitorFactory is the default factory that creates jarvis tx monitors.
// If the reader is a readerAdapter (wrapping a jarvis reader), it uses the underlying reader.
// Otherwise, it returns nil (custom implementations should provide their own monitor factory).
func DefaultTxMonitorFactory(r EthReader) TxMonitor {
	if jarvisReader := jarvisReaderFromInterface(r); jarvisReader != nil {
		return NewTxMonitorAdapter(monitor.NewGenericTxMonitor(jarvisReader))
	}
	return nil
}

// DefaultNetworkResolver is the default resolver that uses jarvis networks.GetNetworkByID.
// This supports standard EVM networks (Ethereum, Polygon, Arbitrum, Optimism, etc.).
// For custom networks, use WithNetworkResolver to provide a custom resolver.
func DefaultNetworkResolver(chainID uint64) (networks.Network, error) {
	return networks.GetNetworkByID(chainID)
}

// jarvisReaderFromInterface extracts the underlying jarvis reader if available
// This is needed for some internal operations that require the concrete type
func jarvisReaderFromInterface(r EthReader) *reader.EthReader {
	if adapter, ok := r.(*readerAdapter); ok {
		return adapter.reader
	}
	return nil
}
