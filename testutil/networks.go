package testutil

import (
	"time"
)

// ============================================================
// Mock Network Implementations
// ============================================================

// MockNetwork is a configurable mock network for testing
type MockNetwork struct {
	ChainIDValue       uint64
	NameValue          string
	SyncTxSupported    bool
	BlockTimeValue     time.Duration
	GasPriceValue      float64
	NodeVariableName   string
	NativeTokenSymbol  string
	NativeTokenDecimal uint64
}

// NewMockNetwork creates a new mock network with default values
func NewMockNetwork(chainID uint64, name string, syncTxSupported bool) *MockNetwork {
	return &MockNetwork{
		ChainIDValue:       chainID,
		NameValue:          name,
		SyncTxSupported:    syncTxSupported,
		BlockTimeValue:     12 * time.Second,
		GasPriceValue:      20.0,
		NodeVariableName:   "MOCK_NODE",
		NativeTokenSymbol:  "ETH",
		NativeTokenDecimal: 18,
	}
}

// NewMockNetworkSyncTx creates a mock network that supports sync transactions (like Arbitrum)
func NewMockNetworkSyncTx(chainID uint64, name string) *MockNetwork {
	return &MockNetwork{
		ChainIDValue:       chainID,
		NameValue:          name,
		SyncTxSupported:    true,
		BlockTimeValue:     250 * time.Millisecond, // Fast block time like L2s
		GasPriceValue:      0.1,                    // Low gas price like L2s
		NodeVariableName:   "MOCK_SYNC_NODE",
		NativeTokenSymbol:  "ETH",
		NativeTokenDecimal: 18,
	}
}

// NewMockNetworkNoSyncTx creates a mock network that doesn't support sync transactions
func NewMockNetworkNoSyncTx(chainID uint64, name string) *MockNetwork {
	return &MockNetwork{
		ChainIDValue:       chainID,
		NameValue:          name,
		SyncTxSupported:    false,
		BlockTimeValue:     12 * time.Second,
		GasPriceValue:      20.0,
		NodeVariableName:   "MOCK_NODE",
		NativeTokenSymbol:  "ETH",
		NativeTokenDecimal: 18,
	}
}

// Network interface implementation

func (m *MockNetwork) GetName() string                            { return m.NameValue }
func (m *MockNetwork) GetChainID() uint64                         { return m.ChainIDValue }
func (m *MockNetwork) GetAlternativeNames() []string              { return nil }
func (m *MockNetwork) GetNativeTokenSymbol() string               { return m.NativeTokenSymbol }
func (m *MockNetwork) GetNativeTokenDecimal() uint64              { return m.NativeTokenDecimal }
func (m *MockNetwork) GetBlockTime() time.Duration                { return m.BlockTimeValue }
func (m *MockNetwork) GetNodeVariableName() string                { return m.NodeVariableName }
func (m *MockNetwork) GetDefaultNodes() map[string]string         { return nil }
func (m *MockNetwork) GetBlockExplorerAPIKeyVariableName() string { return "" }
func (m *MockNetwork) GetBlockExplorerAPIURL() string             { return "" }
func (m *MockNetwork) RecommendedGasPrice() (float64, error)      { return m.GasPriceValue, nil }
func (m *MockNetwork) GetABIString(address string) (string, error) {
	return "", nil
}
func (m *MockNetwork) IsSyncTxSupported() bool   { return m.SyncTxSupported }
func (m *MockNetwork) MultiCallContract() string { return "" }
func (m *MockNetwork) MarshalJSON() ([]byte, error) {
	return []byte(`{"chainID":` + string(rune(m.ChainIDValue)) + `}`), nil
}
func (m *MockNetwork) UnmarshalJSON([]byte) error { return nil }
