package walletarmy

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/tranvictor/jarvis/networks"
)

// setPendingNonce sets the pending nonce for a wallet on a network
func (wm *WalletManager) setPendingNonce(wallet common.Address, network networks.Network, nonce uint64) {
	wm.nonceTracker.SetPendingNonce(wallet, network.GetChainID(), network.GetName(), nonce)
}

// pendingNonce returns the pending nonce for a wallet on a network
func (wm *WalletManager) pendingNonce(wallet common.Address, network networks.Network) *big.Int {
	return wm.nonceTracker.GetPendingNonce(wallet, network.GetChainID())
}

// acquireNonce atomically determines and reserves the next nonce for a transaction.
// This prevents race conditions where multiple concurrent transactions could get the same nonce.
// The nonce is reserved immediately, so even if the transaction fails, subsequent calls will get
// a different nonce. Use ReleaseNonce if you need to release an unused nonce.
//
// Logic:
//  1. Get remote pending nonce and mined nonce from the network
//  2. Compare with local pending nonce to determine the correct next nonce
//  3. Atomically reserve the nonce by incrementing local pending nonce
//  4. Persist the nonce acquisition (if persistence is enabled)
//  5. Return the reserved nonce
func (wm *WalletManager) acquireNonce(wallet common.Address, network networks.Network) (*big.Int, error) {
	// Get remote nonces first (before acquiring lock to avoid holding lock during network calls)
	r, err := wm.Reader(network)
	if err != nil {
		return nil, fmt.Errorf("couldn't get reader: %w", err)
	}

	minedNonce, err := r.GetMinedNonce(wallet.Hex())
	if err != nil {
		return nil, fmt.Errorf("couldn't get mined nonce in context manager: %s", err)
	}

	remotePendingNonce, err := r.GetPendingNonce(wallet.Hex())
	if err != nil {
		return nil, fmt.Errorf("couldn't get remote pending nonce in context manager: %s", err)
	}

	// Delegate to the nonce tracker
	result, err := wm.nonceTracker.AcquireNonce(
		wallet,
		network.GetChainID(),
		network.GetName(),
		minedNonce,
		remotePendingNonce,
	)
	if err != nil {
		return nil, err
	}

	// Persist nonce acquisition for crash recovery
	wm.persistNonceAcquisition(wallet, network.GetChainID(), result.Nonce)

	return big.NewInt(int64(result.Nonce)), nil
}

// ReleaseNonce releases a previously acquired nonce that was not used.
// This allows the nonce to be reused by subsequent transactions.
// Note: This only affects local tracking. If the transaction was already broadcast
// to some nodes, calling this may cause issues.
func (wm *WalletManager) ReleaseNonce(wallet common.Address, network networks.Network, nonce uint64) {
	wm.nonceTracker.ReleaseNonce(wallet, network.GetChainID(), network.GetName(), nonce)
	// Persist nonce release for crash recovery
	wm.persistNonceRelease(wallet, network.GetChainID(), nonce)
}

// nonce is deprecated: use acquireNonce instead for race-safe nonce acquisition.
// This function is kept for backward compatibility but has race conditions
// when called concurrently for the same wallet/network.
func (wm *WalletManager) nonce(wallet common.Address, network networks.Network) (*big.Int, error) {
	return wm.acquireNonce(wallet, network)
}

// persistNonceAcquisition persists a nonce acquisition to the nonce store
func (wm *WalletManager) persistNonceAcquisition(wallet common.Address, chainID uint64, nonce uint64) {
	if wm.nonceStore == nil {
		return
	}
	ctx := context.Background()
	_ = wm.nonceStore.AddReservedNonce(ctx, wallet, chainID, nonce)
	_ = wm.nonceStore.SavePendingNonce(ctx, wallet, chainID, nonce)
}

// persistNonceRelease persists a nonce release to the nonce store
func (wm *WalletManager) persistNonceRelease(wallet common.Address, chainID uint64, nonce uint64) {
	if wm.nonceStore == nil {
		return
	}
	ctx := context.Background()
	_ = wm.nonceStore.RemoveReservedNonce(ctx, wallet, chainID, nonce)
}
