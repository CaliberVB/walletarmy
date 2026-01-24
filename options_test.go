package walletarmy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/walletarmy/idempotency"
)

func TestWithIdempotencyStore(t *testing.T) {
	store := idempotency.NewInMemoryStore(time.Hour)
	wm := NewWalletManager(WithIdempotencyStore(store))

	assert.Equal(t, store, wm.IdempotencyStore())
}

func TestWithDefaultIdempotencyStore(t *testing.T) {
	ttl := 30 * time.Minute
	wm := NewWalletManager(WithDefaultIdempotencyStore(ttl))

	assert.NotNil(t, wm.IdempotencyStore())
}

func TestWithDefaultNumRetries(t *testing.T) {
	wm := NewWalletManager(WithDefaultNumRetries(10))

	defaults := wm.Defaults()
	assert.Equal(t, 10, defaults.NumRetries)
}

func TestWithDefaultSleepDuration(t *testing.T) {
	duration := 5 * time.Second
	wm := NewWalletManager(WithDefaultSleepDuration(duration))

	defaults := wm.Defaults()
	assert.Equal(t, duration, defaults.SleepDuration)
}

func TestWithDefaultTxCheckInterval(t *testing.T) {
	interval := 3 * time.Second
	wm := NewWalletManager(WithDefaultTxCheckInterval(interval))

	defaults := wm.Defaults()
	assert.Equal(t, interval, defaults.TxCheckInterval)
}

func TestWithDefaultExtraGasLimit(t *testing.T) {
	wm := NewWalletManager(WithDefaultExtraGasLimit(50000))

	defaults := wm.Defaults()
	assert.Equal(t, uint64(50000), defaults.ExtraGasLimit)
}

func TestWithDefaultExtraGasPrice(t *testing.T) {
	wm := NewWalletManager(WithDefaultExtraGasPrice(5.5))

	defaults := wm.Defaults()
	assert.Equal(t, 5.5, defaults.ExtraGasPrice)
}

func TestWithDefaultExtraTipCap(t *testing.T) {
	wm := NewWalletManager(WithDefaultExtraTipCap(2.0))

	defaults := wm.Defaults()
	assert.Equal(t, 2.0, defaults.ExtraTipCapGwei)
}

func TestWithDefaultMaxGasPrice(t *testing.T) {
	wm := NewWalletManager(WithDefaultMaxGasPrice(500.0))

	defaults := wm.Defaults()
	assert.Equal(t, 500.0, defaults.MaxGasPrice)
}

func TestWithDefaultMaxTipCap(t *testing.T) {
	wm := NewWalletManager(WithDefaultMaxTipCap(50.0))

	defaults := wm.Defaults()
	assert.Equal(t, 50.0, defaults.MaxTipCap)
}

func TestWithDefaultNetwork(t *testing.T) {
	wm := NewWalletManager(WithDefaultNetwork(networks.BSCMainnet))

	defaults := wm.Defaults()
	assert.Equal(t, networks.BSCMainnet, defaults.Network)
}

func TestWithDefaultTxType(t *testing.T) {
	wm := NewWalletManager(WithDefaultTxType(2))

	defaults := wm.Defaults()
	assert.Equal(t, uint8(2), defaults.TxType)
}

func TestWithDefaults_AllAtOnce(t *testing.T) {
	defaults := ManagerDefaults{
		NumRetries:      5,
		SleepDuration:   10 * time.Second,
		TxCheckInterval: 2 * time.Second,
		ExtraGasLimit:   10000,
		ExtraGasPrice:   3.0,
		ExtraTipCapGwei: 1.5,
		MaxGasPrice:     200.0,
		MaxTipCap:       20.0,
		Network:         networks.BSCMainnet,
		TxType:          2,
	}

	wm := NewWalletManager(WithDefaults(defaults))

	retrieved := wm.Defaults()
	assert.Equal(t, defaults.NumRetries, retrieved.NumRetries)
	assert.Equal(t, defaults.SleepDuration, retrieved.SleepDuration)
	assert.Equal(t, defaults.TxCheckInterval, retrieved.TxCheckInterval)
	assert.Equal(t, defaults.ExtraGasLimit, retrieved.ExtraGasLimit)
	assert.Equal(t, defaults.ExtraGasPrice, retrieved.ExtraGasPrice)
	assert.Equal(t, defaults.ExtraTipCapGwei, retrieved.ExtraTipCapGwei)
	assert.Equal(t, defaults.MaxGasPrice, retrieved.MaxGasPrice)
	assert.Equal(t, defaults.MaxTipCap, retrieved.MaxTipCap)
	assert.Equal(t, defaults.Network, retrieved.Network)
	assert.Equal(t, defaults.TxType, retrieved.TxType)
}

func TestWithReaderFactory(t *testing.T) {
	called := false
	factory := func(network networks.Network) (EthReader, error) {
		called = true
		return createDefaultMockReader(), nil
	}

	wm := NewWalletManager(
		WithReaderFactory(factory),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return &mockEthBroadcaster{}, nil
		}),
		WithTxMonitorFactory(func(reader EthReader) TxMonitor {
			return &mockTxMonitor{}
		}),
	)

	// Trigger the factory by requesting a reader
	_, err := wm.Reader(networks.EthereumMainnet)

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestWithBroadcasterFactory(t *testing.T) {
	called := false
	factory := func(network networks.Network) (EthBroadcaster, error) {
		called = true
		return &mockEthBroadcaster{}, nil
	}

	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return createDefaultMockReader(), nil
		}),
		WithBroadcasterFactory(factory),
		WithTxMonitorFactory(func(reader EthReader) TxMonitor {
			return &mockTxMonitor{}
		}),
	)

	// Trigger the factory by requesting a broadcaster
	_, err := wm.Broadcaster(networks.EthereumMainnet)

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestWithTxMonitorFactory(t *testing.T) {
	called := false
	factory := func(reader EthReader) TxMonitor {
		called = true
		return &mockTxMonitor{}
	}

	wm := NewWalletManager(
		WithReaderFactory(func(network networks.Network) (EthReader, error) {
			return createDefaultMockReader(), nil
		}),
		WithBroadcasterFactory(func(network networks.Network) (EthBroadcaster, error) {
			return &mockEthBroadcaster{}, nil
		}),
		WithTxMonitorFactory(factory),
	)

	// Trigger network initialization
	_, err := wm.Reader(networks.EthereumMainnet)

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMultipleOptions_CombineCorrectly(t *testing.T) {
	wm := NewWalletManager(
		WithDefaultNumRetries(3),
		WithDefaultSleepDuration(5*time.Second),
		WithDefaultNetwork(networks.BSCMainnet),
		WithDefaultMaxGasPrice(100.0),
	)

	defaults := wm.Defaults()
	assert.Equal(t, 3, defaults.NumRetries)
	assert.Equal(t, 5*time.Second, defaults.SleepDuration)
	assert.Equal(t, networks.BSCMainnet, defaults.Network)
	assert.Equal(t, 100.0, defaults.MaxGasPrice)
}

func TestOptions_LaterOverridesEarlier(t *testing.T) {
	wm := NewWalletManager(
		WithDefaultNumRetries(3),
		WithDefaultNumRetries(5),
		WithDefaultNumRetries(10),
	)

	defaults := wm.Defaults()
	assert.Equal(t, 10, defaults.NumRetries) // Last value wins
}

func TestNewWalletManager_WithNoOptions(t *testing.T) {
	wm := NewWalletManager()

	assert.NotNil(t, wm)
	assert.NotNil(t, wm.readerFactory)
	assert.NotNil(t, wm.broadcasterFactory)
	assert.NotNil(t, wm.txMonitorFactory)
	assert.Nil(t, wm.IdempotencyStore())
}

func TestNewWalletManager_SetsDefaultFactories(t *testing.T) {
	wm := NewWalletManager()

	// Verify default factories are set (they won't be nil)
	assert.NotNil(t, wm.readerFactory)
	assert.NotNil(t, wm.broadcasterFactory)
	assert.NotNil(t, wm.txMonitorFactory)
	assert.NotNil(t, wm.networkResolver)
}

func TestWithNetworkResolver(t *testing.T) {
	customChainID := uint64(12345)
	customNetwork := networks.EthereumMainnet // Use a real network for testing

	called := false
	resolver := func(chainID uint64) (networks.Network, error) {
		called = true
		if chainID == customChainID {
			return customNetwork, nil
		}
		return networks.GetNetworkByID(chainID)
	}

	wm := NewWalletManager(
		WithNetworkResolver(resolver),
	)

	// Trigger resolver call
	network, err := wm.getNetworkByChainID(customChainID)

	assert.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, customNetwork, network)
}

func TestDefaultNetworkResolver_UsesJarvisNetworks(t *testing.T) {
	wm := NewWalletManager()

	// Default resolver should be able to resolve standard networks
	network, err := wm.getNetworkByChainID(1) // Ethereum mainnet

	assert.NoError(t, err)
	assert.Equal(t, uint64(1), network.GetChainID())
}

func TestDefaultNetworkResolver_ErrorsForUnknownChainID(t *testing.T) {
	wm := NewWalletManager()

	// Default resolver should return error for unknown chain IDs
	_, err := wm.getNetworkByChainID(999999999)

	assert.Error(t, err)
}
