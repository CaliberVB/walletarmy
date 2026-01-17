package walletarmy

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/core/types"
)

// Hook is a function that is called before and after a transaction is signed and broadcasted
type Hook func(tx *types.Transaction, err error) error

// TxMinedHook is called when a transaction is mined (either successfully or reverted).
// The receipt contains the transaction result including status.
// Return an error to propagate it to the caller.
type TxMinedHook func(tx *types.Transaction, receipt *types.Receipt) error

// SimulationFailedHook is called when eth_call simulation fails (the tx would revert).
// This is different from GasEstimationFailedHook which is for gas estimation failures.
// The revertData contains the raw revert data from the node.
// If the hook returns shouldRetry=true, the execution will continue retrying.
// Return an error to stop execution immediately.
type SimulationFailedHook func(tx *types.Transaction, revertData []byte, abiError *abi.Error, revertParams any, err error) (shouldRetry bool, retErr error)

// GasEstimationFailedHook is a function that is called when a transaction is failed to estimate gas
// revertMsg is the revert message returned by the contract and parsed with the abi passed in the tx request
// This hook will NOT be called if the tx request doesn't have any abis set
// If the hook returns a non-nil gas limit, the gas limit will be used to do the next iteration
// The hook should return an error to stop the loop and return the error
type GasEstimationFailedHook func(tx *types.Transaction, abiError *abi.Error, revertParams any, revertMsgError, gasEstimationError error) (gasLimit *big.Int, err error)
