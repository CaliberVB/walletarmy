// Package testutil provides testing utilities for walletarmy.
//
// This package contains test fixtures, transaction builders, and mock networks
// that are commonly used across tests in the walletarmy package.
//
// # Important Note on Import Cycles
//
// Mock implementations (mockEthReader, mockEthBroadcaster, mockTxMonitor, etc.)
// are kept in the walletarmy package's test files (testing_mocks_test.go) to avoid
// import cycles. This package only contains utilities that don't depend on
// walletarmy types.
//
// # Test Fixtures
//
// Common test values are provided:
//   - TestAddr1, TestAddr2, TestAddr3: Common test addresses
//   - TestPrivateKey1, TestPrivateKeyHex, TestPrivateKey1Address: Test private keys and derived address
//   - OneEth, TwentyGwei, TwoGwei: Common value constants
//   - ChainIDMainnet, ChainIDArbitrum: Common chain IDs
//
// # Transaction Builders
//
// Helper functions for creating test transactions:
//   - NewTx: Create a simple mainnet transaction
//   - NewTxWithChainID: Create a transaction with specific chain ID
//   - NewDynamicTx: Create a fully customized EIP-1559 transaction
//   - NewSuccessReceipt, NewFailedReceipt: Create test receipts
//
// # Mock Networks
//
// Configurable mock network implementations:
//   - NewMockNetwork: Create a configurable mock network
//   - NewMockNetworkSyncTx: Create a mock network with sync tx support (like Arbitrum)
//   - NewMockNetworkNoSyncTx: Create a mock network without sync tx support
//
// # Example Usage
//
//	func TestMyFunction(t *testing.T) {
//	    // Create test transaction using testutil fixtures and builders
//	    tx := testutil.NewTx(0, testutil.TestAddr2, testutil.OneEth)
//
//	    // Create mock network
//	    network := testutil.NewMockNetworkSyncTx(42161, "mock-arbitrum")
//
//	    // Run test
//	    // ...
//	}
package testutil
