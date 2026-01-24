package testutil

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ============================================================
// Transaction Builders
// ============================================================

// NewDynamicTx creates a new EIP-1559 dynamic fee transaction for testing
func NewDynamicTx(nonce uint64, to common.Address, value *big.Int, gasLimit uint64, gasTipCap, gasFeeCap *big.Int, chainID *big.Int) *types.Transaction {
	return types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &to,
		Value:     value,
		Data:      nil,
	})
}

// NewTx creates a simple test transaction with default gas settings on mainnet
func NewTx(nonce uint64, to common.Address, value *big.Int) *types.Transaction {
	return NewDynamicTx(nonce, to, value, 21000, TwoGwei, TwentyGwei, ChainIDMainnet)
}

// NewTxWithChainID creates a test transaction with a specific chain ID
func NewTxWithChainID(nonce uint64, to common.Address, value *big.Int, chainID *big.Int) *types.Transaction {
	return NewDynamicTx(nonce, to, value, 21000, TwoGwei, TwentyGwei, chainID)
}

// NewLegacyTx creates a legacy (pre-EIP-1559) transaction for testing
func NewLegacyTx(nonce uint64, to common.Address, value *big.Int, gasLimit uint64, gasPrice *big.Int, chainID *big.Int) *types.Transaction {
	return types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      gasLimit,
		To:       &to,
		Value:    value,
		Data:     nil,
	})
}

// ============================================================
// Receipt Builders
// ============================================================

// NewReceipt creates a test receipt for a transaction with a specific status
func NewReceipt(tx *types.Transaction, status uint64) *types.Receipt {
	return &types.Receipt{
		Status:            status,
		TxHash:            tx.Hash(),
		BlockNumber:       big.NewInt(12345678),
		BlockHash:         common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
		TransactionIndex:  0,
		GasUsed:           tx.Gas(),
		CumulativeGasUsed: tx.Gas(),
	}
}

// NewSuccessReceipt creates a successful receipt for a transaction
func NewSuccessReceipt(tx *types.Transaction) *types.Receipt {
	return NewReceipt(tx, types.ReceiptStatusSuccessful)
}

// NewFailedReceipt creates a failed (reverted) receipt for a transaction
func NewFailedReceipt(tx *types.Transaction) *types.Receipt {
	return NewReceipt(tx, types.ReceiptStatusFailed)
}

// NewReceiptWithBlockNumber creates a receipt with a specific block number
func NewReceiptWithBlockNumber(tx *types.Transaction, status uint64, blockNumber int64) *types.Receipt {
	receipt := NewReceipt(tx, status)
	receipt.BlockNumber = big.NewInt(blockNumber)
	return receipt
}
