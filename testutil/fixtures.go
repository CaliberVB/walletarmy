package testutil

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ============================================================
// Test Addresses
// ============================================================

var (
	// TestAddr1 is a common test address for "from" addresses
	TestAddr1 = common.HexToAddress("0x1111111111111111111111111111111111111111")
	// TestAddr2 is a common test address for "to" addresses
	TestAddr2 = common.HexToAddress("0x2222222222222222222222222222222222222222")
	// TestAddr3 is an additional test address
	TestAddr3 = common.HexToAddress("0x3333333333333333333333333333333333333333")
)

// ============================================================
// Test Private Keys
// ============================================================

var (
	// TestPrivateKeyHex is a test private key in hex format
	TestPrivateKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	// TestPrivateKey1 is a parsed ECDSA private key for testing
	TestPrivateKey1, _ = crypto.HexToECDSA(TestPrivateKeyHex)
	// TestPrivateKey1Address is the address derived from TestPrivateKey1
	TestPrivateKey1Address = crypto.PubkeyToAddress(TestPrivateKey1.PublicKey)
)

// ============================================================
// Common Values
// ============================================================

var (
	// OneEth represents 1 ETH in wei
	OneEth = big.NewInt(1000000000000000000)
	// TwentyGwei represents 20 gwei
	TwentyGwei = big.NewInt(20000000000)
	// TwoGwei represents 2 gwei
	TwoGwei = big.NewInt(2000000000)
)

// ============================================================
// Chain IDs
// ============================================================

var (
	// ChainIDMainnet is the chain ID for Ethereum mainnet
	ChainIDMainnet = big.NewInt(1)
	// ChainIDArbitrum is the chain ID for Arbitrum mainnet
	ChainIDArbitrum = big.NewInt(42161)
)
