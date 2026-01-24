// Package testutil provides testing utilities for walletarmy.
// This package is intended for use in tests only and should not be imported in production code.
//
// Note: Mock implementations that need to implement walletarmy interfaces are kept in
// the walletarmy package (testing_mocks_test.go) to avoid import cycles.
// This package only contains fixtures, builders, and other utilities that don't
// depend on walletarmy types.
package testutil

// Mock implementations are in the walletarmy package (testing_mocks_test.go)
// to avoid import cycles. See that file for:
// - mockEthReader
// - mockEthBroadcaster
// - mockTxMonitor
// - mockNonceStore
// - mockTxStore
