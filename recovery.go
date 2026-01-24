package walletarmy

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// RecoveryHandler manages the recovery process for a WalletManager
type RecoveryHandler struct {
	wm         *WalletManager
	txStore    TxStore
	nonceStore NonceStore
}

// newRecoveryHandler creates a new recovery handler
func newRecoveryHandler(wm *WalletManager) *RecoveryHandler {
	return &RecoveryHandler{
		wm:         wm,
		txStore:    wm.txStore,
		nonceStore: wm.nonceStore,
	}
}

// Recover performs recovery after a crash or restart.
// It reconciles nonce state with the chain and resumes monitoring pending transactions.
//
// This method should be called once during application startup, before processing new transactions.
func (rh *RecoveryHandler) Recover(ctx context.Context, opts RecoveryOptions) (*RecoveryResult, error) {
	result := &RecoveryResult{}

	// Step 1: Reconcile nonce state
	if rh.nonceStore != nil {
		if err := rh.reconcileNonceState(ctx, result); err != nil {
			return result, err
		}
	}

	// Step 2: Process pending transactions
	if rh.txStore != nil {
		if err := rh.processPendingTransactions(ctx, opts, result); err != nil {
			return result, err
		}
	}

	return result, nil
}

// reconcileNonceState reconciles local nonce state with chain state
func (rh *RecoveryHandler) reconcileNonceState(ctx context.Context, result *RecoveryResult) error {
	states, err := rh.nonceStore.ListAll(ctx)
	if err != nil {
		return err
	}

	for _, state := range states {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Get chain state
		network, err := rh.wm.getNetworkByChainID(state.ChainID)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		reader, err := rh.wm.Reader(network)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		minedNonce, err := reader.GetMinedNonce(state.Wallet.Hex())
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		remotePending, err := reader.GetPendingNonce(state.Wallet.Hex())
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		// Reconcile: use the max of mined, remote pending, and local state
		var reconciledNonce uint64
		if state.LocalPendingNonce != nil {
			reconciledNonce = *state.LocalPendingNonce
		}
		if minedNonce > reconciledNonce {
			reconciledNonce = minedNonce
		}
		if remotePending > reconciledNonce {
			reconciledNonce = remotePending
		}

		// Update internal tracker with reconciled state
		// We set to reconciledNonce-1 because the tracker stores the "last used" nonce,
		// and GetPendingNonceUnlocked adds 1 to get the "next to use"
		if reconciledNonce > 0 {
			rh.wm.nonceTracker.SetPendingNonce(state.Wallet, state.ChainID, network.GetName(), reconciledNonce-1)
		}

		// Clear reserved nonces that are below mined nonce (they're confirmed)
		for _, reserved := range state.ReservedNonces {
			if reserved < minedNonce {
				_ = rh.nonceStore.RemoveReservedNonce(ctx, state.Wallet, state.ChainID, reserved)
			}
		}

		result.ReconciledNonces++
	}

	return nil
}

// processPendingTransactions checks status of pending transactions and optionally resumes monitoring
func (rh *RecoveryHandler) processPendingTransactions(ctx context.Context, opts RecoveryOptions, result *RecoveryResult) error {
	pendingTxs, err := rh.txStore.ListAllPending(ctx)
	if err != nil {
		return err
	}

	// Use a semaphore for concurrent monitoring
	sem := make(chan struct{}, opts.MaxConcurrentMonitors)

	for _, ptx := range pendingTxs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Get network for this transaction
		network, err := rh.wm.getNetworkByChainID(ptx.ChainID)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		// Check current status on chain
		reader, err := rh.wm.Reader(network)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		txInfo, err := reader.TxInfoFromHash(ptx.Hash.Hex())
		if err != nil {
			// Transaction not found - might be dropped
			result.DroppedTxs++
			_ = rh.txStore.UpdateStatus(ctx, ptx.Hash, PendingTxStatusDropped, nil)
			if opts.OnTxDropped != nil {
				opts.OnTxDropped(ptx)
			}
			continue
		}

		switch txInfo.Status {
		case "done":
			result.MinedTxs++
			_ = rh.txStore.UpdateStatus(ctx, ptx.Hash, PendingTxStatusMined, txInfo.Receipt)
			if opts.OnTxMined != nil {
				opts.OnTxMined(ptx, txInfo.Receipt)
			}
		case "reverted":
			result.MinedTxs++
			_ = rh.txStore.UpdateStatus(ctx, ptx.Hash, PendingTxStatusReverted, txInfo.Receipt)
			if opts.OnTxMined != nil {
				opts.OnTxMined(ptx, txInfo.Receipt)
			}
		case "pending":
			result.RecoveredTxs++
			if opts.OnTxRecovered != nil {
				opts.OnTxRecovered(ptx)
			}

			// Resume the full EnsureTx flow (with gas bumping and retry logic)
			if opts.ResumeMonitoring && ptx.Transaction != nil {
				sem <- struct{}{}
				go func(tx *PendingTx) {
					defer func() { <-sem }()
					rh.resumeTransaction(ctx, tx, opts)
				}(ptx)
			}
		default:
			// Unknown status, mark as dropped
			result.DroppedTxs++
			_ = rh.txStore.UpdateStatus(ctx, ptx.Hash, PendingTxStatusDropped, nil)
		}
	}

	return nil
}

// resumeTransaction resumes a recovered pending transaction using the full EnsureTx flow.
// This provides the same gas bumping and retry logic as the main execution path.
func (rh *RecoveryHandler) resumeTransaction(ctx context.Context, ptx *PendingTx, opts RecoveryOptions) {
	// Build resume options from recovery options
	resumeOpts := ResumeTransactionOptions{
		TxCheckInterval: opts.TxCheckInterval,
		TxMinedHook: func(tx *types.Transaction, receipt *types.Receipt) error {
			// Update store status
			if receipt.Status == types.ReceiptStatusSuccessful {
				_ = rh.txStore.UpdateStatus(ctx, tx.Hash(), PendingTxStatusMined, receipt)
			} else {
				_ = rh.txStore.UpdateStatus(ctx, tx.Hash(), PendingTxStatusReverted, receipt)
			}
			// Call user callback
			if opts.OnTxMined != nil {
				opts.OnTxMined(ptx, receipt)
			}
			return nil
		},
	}

	// Use the full ResumePendingTransaction flow which includes gas bumping and retry logic
	finalTx, receipt, err := rh.wm.ResumePendingTransaction(ctx, ptx, resumeOpts)

	if err != nil {
		// Transaction failed after retries - mark as dropped
		_ = rh.txStore.UpdateStatus(ctx, ptx.Hash, PendingTxStatusDropped, nil)
		if opts.OnTxDropped != nil {
			opts.OnTxDropped(ptx)
		}
		return
	}

	// If we got a different transaction (due to replacement), update the original as replaced
	if finalTx != nil && finalTx.Hash() != ptx.Hash {
		_ = rh.txStore.UpdateStatus(ctx, ptx.Hash, PendingTxStatusReplaced, nil)
		// Save the new transaction
		newPtx := &PendingTx{
			Hash:        finalTx.Hash(),
			Wallet:      ptx.Wallet,
			ChainID:     ptx.ChainID,
			Nonce:       finalTx.Nonce(),
			Transaction: finalTx,
			Receipt:     receipt,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if receipt != nil {
			if receipt.Status == types.ReceiptStatusSuccessful {
				newPtx.Status = PendingTxStatusMined
			} else {
				newPtx.Status = PendingTxStatusReverted
			}
		} else {
			newPtx.Status = PendingTxStatusBroadcasted
		}
		_ = rh.txStore.Save(ctx, newPtx)
	}
}
