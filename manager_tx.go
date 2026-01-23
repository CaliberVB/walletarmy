package walletarmy

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/KyberNetwork/logger"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	jarviscommon "github.com/tranvictor/jarvis/common"
	"github.com/tranvictor/jarvis/networks"
)

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

func (wm *WalletManager) getTxStatuses(oldTxs map[string]*types.Transaction, network networks.Network) (statuses map[string]TxInfo, err error) {
	result := map[string]TxInfo{}

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
		if err != nil && len(execCtx.OldTxs) == 0 && execCtx.RetryNonce != nil {
			// We have a reserved nonce but no tx was ever broadcast - release it
			wm.ReleaseNonce(from, network, execCtx.RetryNonce.Uint64())
		}
	}()

	// Create error decoder
	errDecoder := wm.createErrorDecoder(execCtx.ABIs)

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
		if execCtx.ActualRetryCount > 0 {
			// Use a timer so we can also check for context cancellation during sleep
			sleepTimer := time.NewTimer(execCtx.SleepDuration)
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
			statusChan := wm.MonitorTxContext(ctx, result.Transaction, execCtx.Network, execCtx.TxCheckInterval)

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
		execCtx.TxType,
		execCtx.From,
		execCtx.To,
		execCtx.RetryNonce,
		execCtx.Value,
		execCtx.GasLimit,
		execCtx.ExtraGasLimit,
		execCtx.RetryGasPrice,
		execCtx.ExtraGasPrice,
		execCtx.RetryTipCap,
		execCtx.ExtraTipCapGwei,
		execCtx.Data,
		execCtx.Network,
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
	r, readerErr := wm.Reader(execCtx.Network)
	if readerErr != nil {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        errors.Join(ErrSimulatedTxFailed, fmt.Errorf("couldn't get reader for simulation: %w", readerErr)),
		}
	}
	_, err = r.EthCall(execCtx.From.Hex(), execCtx.To.Hex(), execCtx.Data, nil) // nil overrides = use current state
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
				"used_sync_tx":    execCtx.Network.IsSyncTxSupported(),
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
		"used_sync_tx":    execCtx.Network.IsSyncTxSupported(),
		"error":           err,
		"revert_data":     revertData,
	}).Debug("Tx simulation showed a revert error")

	// Call simulation failed hook if set
	if execCtx.SimulationFailedHook != nil {
		shouldRetry, hookErr := execCtx.SimulationFailedHook(builtTx, revertData, abiError, revertParams, err)
		if hookErr != nil {
			// Release nonce since we're giving up and tx was never broadcast
			wm.ReleaseNonce(execCtx.From, execCtx.Network, builtTx.Nonce())
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        fmt.Errorf("simulation failed hook error: %w", hookErr),
			}
		}
		if !shouldRetry {
			// Release nonce since we're giving up and tx was never broadcast
			wm.ReleaseNonce(execCtx.From, execCtx.Network, builtTx.Nonce())
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        err,
			}
		}
	}

	// we need to persist a few calculated values here before retrying with the txs
	execCtx.RetryNonce = big.NewInt(int64(builtTx.Nonce()))
	execCtx.GasLimit = builtTx.Gas()
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
	if len(execCtx.OldTxs) > 0 {
		// Check if previous transactions might have been successful
		statuses, statusErr := wm.getTxStatuses(execCtx.OldTxs, execCtx.Network)
		if statusErr != nil {
			logger.WithFields(logger.Fields{
				"error": statusErr,
			}).Debug("Getting tx statuses after gas estimation failure. Ignore and continue the retry loop")
			// Don't return immediately, proceed to hook handling
		} else {
			// Check for completed transactions
			for txhash, status := range statuses {
				if status.Status == "done" || status.Status == "reverted" {
					if tx, exists := execCtx.OldTxs[txhash]; exists && tx != nil {
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
					if tx, exists := execCtx.OldTxs[txhash]; exists && tx != nil {
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
	if errDecoder != nil && execCtx.GasEstimationFailedHook != nil {
		abiError, revertParams, revertMsgErr := errDecoder.Decode(err)
		hookGasLimit, hookErr := execCtx.GasEstimationFailedHook(nil, abiError, revertParams, revertMsgErr, err)
		if hookErr != nil {
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        hookErr,
			}
		}
		if hookGasLimit != nil {
			execCtx.GasLimit = hookGasLimit.Uint64()
		}
	}

	// Increment retry count for gas estimation failure
	execCtx.ActualRetryCount++
	if execCtx.ActualRetryCount > execCtx.NumRetries {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        errors.Join(ErrEnsureTxOutOfRetries, fmt.Errorf("gas estimation failed after %d retries", execCtx.NumRetries)),
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
		// Only release if this tx is not in OldTxs (meaning it was never broadcast)
		if _, exists := execCtx.OldTxs[tx.Hash().Hex()]; !exists {
			wm.ReleaseNonce(execCtx.From, execCtx.Network, tx.Nonce())
		}
	}

	// Execute before hook
	if execCtx.BeforeSignAndBroadcastHook != nil {
		if hookError := execCtx.BeforeSignAndBroadcastHook(tx, nil); hookError != nil {
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
	signedAddr, signedTx, err := wm.SignTx(execCtx.From, tx, execCtx.Network)
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
	if signedAddr.Cmp(execCtx.From) != 0 {
		releaseNonce()
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error: fmt.Errorf(
				"signed from wrong address. You could use wrong hw or passphrase. Expected wallet: %s, signed wallet: %s",
				execCtx.From.Hex(),
				signedAddr.Hex(),
			),
		}
	}

	var receipt *types.Receipt
	var broadcastErr BroadcastError
	var successful bool

	// Broadcast transaction
	// if execCtx.Network.IsSyncTxSupported() {
	// 	receipt, broadcastErr = wm.BroadcastTxSync(signedTx)
	// 	if receipt != nil {
	// 		successful = true
	// 	}
	// } else {
	_, successful, broadcastErr = wm.BroadcastTx(signedTx)
	// }

	if signedTx != nil {
		execCtx.OldTxs[signedTx.Hash().Hex()] = signedTx
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
				"used_sync_tx":    execCtx.Network.IsSyncTxSupported(),
				"receipt":         receipt,
				"error":           broadcastErr,
			}).Debug("Unsuccessful signing and broadcasting transaction")
		} else {
			logger.WithFields(logger.Fields{
				"nonce":        tx.Nonce(),
				"gas_price":    tx.GasPrice().String(),
				"used_sync_tx": execCtx.Network.IsSyncTxSupported(),
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
		"used_sync_tx":    execCtx.Network.IsSyncTxSupported(),
		"receipt":         receipt,
	}).Info("Signed and broadcasted transaction")

	// Execute after hook - convert BroadcastError to error for hook
	var hookErr error
	if broadcastErr != nil {
		hookErr = broadcastErr
	}

	if execCtx.AfterSignAndBroadcastHook != nil {
		if hookError := execCtx.AfterSignAndBroadcastHook(signedTx, hookErr); hookError != nil {
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
		// Call TxMinedHook if set
		if execCtx.TxMinedHook != nil {
			if hookErr := execCtx.TxMinedHook(signedTx, receipt); hookErr != nil {
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
		execCtx.RetryNonce = big.NewInt(int64(tx.Nonce()))
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

	if result := execCtx.IncrementRetryCountAndCheck(errorMsg); result != nil {
		return result
	}

	// Keep the same nonce for retry
	execCtx.RetryNonce = big.NewInt(int64(tx.Nonce()))
	return &TxExecutionResult{
		Transaction:  nil,
		ShouldRetry:  true,
		ShouldReturn: false,
		Error:        nil,
	}
}

// handleNonceIsLowError specifically handles the nonce is low error case
func (wm *WalletManager) handleNonceIsLowError(tx *types.Transaction, execCtx *TxExecutionContext) *TxExecutionResult {

	statuses, err := wm.getTxStatuses(execCtx.OldTxs, execCtx.Network)

	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Debug("Getting tx statuses in case where tx wasn't broadcasted because nonce is too low. Ignore and continue the retry loop")

		if result := execCtx.IncrementRetryCountAndCheck("nonce is low and status check failed"); result != nil {
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
			if tx, exists := execCtx.OldTxs[txhash]; exists && tx != nil {
				// Call TxMinedHook if set (same as handleTransactionStatus)
				if execCtx.TxMinedHook != nil {
					if hookErr := execCtx.TxMinedHook(tx, status.Receipt); hookErr != nil {
						return &TxExecutionResult{
							Transaction:  tx,
							Receipt:      status.Receipt,
							ShouldRetry:  false,
							ShouldReturn: true,
							Error:        fmt.Errorf("tx mined hook error: %w", hookErr),
						}
					}
				}
				return &TxExecutionResult{
					Transaction:  tx,
					Receipt:      status.Receipt, // Include the receipt!
					ShouldRetry:  false,
					ShouldReturn: true,
					Error:        nil,
				}
			}
		}
	}

	// No completed transactions found, retry with new nonce
	if result := execCtx.IncrementRetryCountAndCheck("nonce is low and no pending transactions"); result != nil {
		return result
	}

	execCtx.RetryNonce = nil

	return &TxExecutionResult{
		Transaction:  nil,
		ShouldRetry:  true,
		ShouldReturn: false,
		Error:        nil,
	}
}

// handleTransactionStatus processes different transaction statuses
func (wm *WalletManager) handleTransactionStatus(status TxStatus, signedTx *types.Transaction, execCtx *TxExecutionContext) *TxExecutionResult {
	switch status.Status {
	case "mined", "reverted":
		// Call TxMinedHook if set
		if execCtx.TxMinedHook != nil {
			if hookErr := execCtx.TxMinedHook(signedTx, status.Receipt); hookErr != nil {
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

		execCtx.ActualRetryCount++
		if execCtx.ActualRetryCount > execCtx.NumRetries {
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        errors.Join(ErrEnsureTxOutOfRetries, fmt.Errorf("transaction lost after %d retries", execCtx.NumRetries)),
			}
		}
		execCtx.RetryNonce = nil

		// Sleep will be handled in main loop based on actualRetryCount
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  true,
			ShouldReturn: false,
			Error:        nil,
		}

	case "slow":
		// Try to adjust gas prices for slow transaction
		if execCtx.AdjustGasPricesForSlowTx(signedTx) {
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
				"max_gas_price": execCtx.MaxGasPrice,
				"max_tip_cap":   execCtx.MaxTipCap,
			}).Warn("Transaction slow but gas price protection limits reached. Stopping retry attempts.")

			return &TxExecutionResult{
				Transaction:  signedTx,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        errors.Join(ErrGasPriceLimitReached, fmt.Errorf("maxGasPrice: %f, maxTipCap: %f", execCtx.MaxGasPrice, execCtx.MaxTipCap)),
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
