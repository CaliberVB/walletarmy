package walletarmy

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/KyberNetwork/logger"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/tranvictor/jarvis/accounts"
	jarviscommon "github.com/tranvictor/jarvis/common"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/txanalyzer"
	"github.com/tranvictor/jarvis/util"
	"github.com/tranvictor/jarvis/util/account"
	"github.com/tranvictor/jarvis/util/broadcaster"
	"github.com/tranvictor/jarvis/util/monitor"
	"github.com/tranvictor/jarvis/util/reader"
)

const (
	DefaultNumRetries      = 9
	DefaultSleepDuration   = 5 * time.Second
	DefaultTxCheckInterval = 5 * time.Second

	// Gas adjustment constants
	GasPriceIncreasePercent = 1.2 // 20% increase
	TipCapIncreasePercent   = 1.1 // 10% increase
	MaxCapMultiplier        = 5.0 // Multiplier for default max gas price/tip cap when not set
)

var (
	ErrEstimateGasFailed    = fmt.Errorf("estimate gas failed")
	ErrAcquireNonceFailed   = fmt.Errorf("acquire nonce failed")
	ErrGetGasSettingFailed  = fmt.Errorf("get gas setting failed")
	ErrEnsureTxOutOfRetries = fmt.Errorf("ensure tx out of retries")
	ErrGasPriceLimitReached = fmt.Errorf("gas price protection limit reached")
	ErrFromAddressZero      = fmt.Errorf("from address cannot be zero")
	ErrNetworkNil           = fmt.Errorf("network cannot be nil")
	ErrSimulatedTxReverted  = fmt.Errorf("tx will be reverted")
	ErrSimulatedTxFailed    = fmt.Errorf("couldn't simulate tx at pending state")
)

// TxExecutionResult represents the outcome of a transaction execution step
type TxExecutionResult struct {
	Transaction  *types.Transaction
	Receipt      *types.Receipt
	ShouldRetry  bool
	ShouldReturn bool
	Error        error
}

// WalletManager manages
//  1. multiple wallets and their informations in its
//     life time. It basically gives next nonce to do transaction for specific
//     wallet and specific network.
//     It queries the node to check the nonce in lazy maner, it also takes mining
//     txs into account.
//  2. multiple networks gas price. The gas price will be queried lazily prior to txs
//     and will be stored as cache for a while
//  3. txs in the context manager's life time
type WalletManager struct {
	lock sync.RWMutex

	// readers stores all reader instances for all networks that ever interacts
	// with accounts manager. ChainID of the network is used as the key.
	readers      map[uint64]*reader.EthReader
	broadcasters map[uint64]*broadcaster.Broadcaster
	analyzers    map[uint64]*txanalyzer.TxAnalyzer
	txMonitors   map[uint64]*monitor.TxMonitor
	accounts     map[common.Address]*account.Account

	// nonces map between (address, network) => last signed nonce (not mined nonces)
	pendingNonces map[common.Address]map[uint64]*big.Int
	// txs map between (address, network, nonce) => tx
	txs map[common.Address]map[uint64]map[uint64]*types.Transaction

	// gasPrices map between network => gasinfo
	gasSettings map[uint64]*GasInfo
}

func NewWalletManager() *WalletManager {
	return &WalletManager{
		lock:          sync.RWMutex{},
		readers:       map[uint64]*reader.EthReader{},
		broadcasters:  map[uint64]*broadcaster.Broadcaster{},
		analyzers:     map[uint64]*txanalyzer.TxAnalyzer{},
		txMonitors:    map[uint64]*monitor.TxMonitor{},
		accounts:      map[common.Address]*account.Account{},
		pendingNonces: map[common.Address]map[uint64]*big.Int{},
		txs:           map[common.Address]map[uint64]map[uint64]*types.Transaction{},
		gasSettings:   map[uint64]*GasInfo{},
	}
}

func (wm *WalletManager) SetAccount(acc *account.Account) {
	wm.lock.Lock()
	defer wm.lock.Unlock()
	wm.accounts[acc.Address()] = acc
}

func (wm *WalletManager) UnlockAccount(addr common.Address) (*account.Account, error) {
	accDesc, err := accounts.GetAccount(addr.Hex())
	if err != nil {
		return nil, fmt.Errorf("wallet %s doesn't exist in jarvis", addr.Hex())
	}
	acc, err := accounts.UnlockAccount(accDesc)
	if err != nil {
		return nil, fmt.Errorf("unlocking wallet failed: %w", err)
	}
	wm.SetAccount(acc)
	return acc, nil
}

func (wm *WalletManager) Account(wallet common.Address) *account.Account {
	wm.lock.RLock()
	defer wm.lock.RUnlock()
	return wm.accounts[wallet]
}

func (wm *WalletManager) setTx(wallet common.Address, network networks.Network, tx *types.Transaction) {
	wm.lock.Lock()
	defer wm.lock.Unlock()

	if wm.txs[wallet] == nil {
		wm.txs[wallet] = map[uint64]map[uint64]*types.Transaction{}
	}

	if wm.txs[wallet][network.GetChainID()] == nil {
		wm.txs[wallet][network.GetChainID()] = map[uint64]*types.Transaction{}
	}

	wm.txs[wallet][network.GetChainID()][uint64(tx.Nonce())] = tx
}

func (wm *WalletManager) getBroadcaster(network networks.Network) *broadcaster.Broadcaster {
	wm.lock.RLock()
	defer wm.lock.RUnlock()
	return wm.broadcasters[network.GetChainID()]
}

func (wm *WalletManager) Broadcaster(network networks.Network) *broadcaster.Broadcaster {
	broadcaster := wm.getBroadcaster(network)
	if broadcaster == nil {
		err := wm.initNetwork(network)
		if err != nil {
			panic(
				fmt.Errorf(
					"couldn't init reader and broadcaster for network: %s, err: %s",
					network,
					err,
				),
			)
		}
		return wm.getBroadcaster(network)
	}
	return broadcaster
}

func (wm *WalletManager) getReader(network networks.Network) *reader.EthReader {
	wm.lock.RLock()
	defer wm.lock.RUnlock()
	return wm.readers[network.GetChainID()]
}

func (wm *WalletManager) Reader(network networks.Network) *reader.EthReader {
	reader := wm.getReader(network)
	if reader == nil {
		err := wm.initNetwork(network)
		if err != nil {
			panic(
				fmt.Errorf(
					"couldn't init reader and broadcaster for network: %s, err: %s",
					network,
					err,
				),
			)
		}
		return wm.getReader(network)
	}
	return reader
}

func (wm *WalletManager) getAnalyzer(network networks.Network) *txanalyzer.TxAnalyzer {
	wm.lock.RLock()
	defer wm.lock.RUnlock()
	return wm.analyzers[network.GetChainID()]
}

func (wm *WalletManager) getTxMonitor(network networks.Network) *monitor.TxMonitor {
	wm.lock.RLock()
	defer wm.lock.RUnlock()
	return wm.txMonitors[network.GetChainID()]
}

func (wm *WalletManager) Analyzer(network networks.Network) *txanalyzer.TxAnalyzer {
	analyzer := wm.getAnalyzer(network)
	if analyzer == nil {
		err := wm.initNetwork(network)
		if err != nil {
			panic(
				fmt.Errorf(
					"couldn't init reader and broadcaster for network: %s, err: %s",
					network,
					err,
				),
			)
		}
		return wm.getAnalyzer(network)
	}
	return analyzer
}

func (wm *WalletManager) initNetwork(network networks.Network) (err error) {
	wm.lock.Lock()
	defer wm.lock.Unlock()

	reader, found := wm.readers[network.GetChainID()]
	if !found {
		reader, err = util.EthReader(network)
		if err != nil {
			return err
		}
	}
	wm.readers[network.GetChainID()] = reader

	analyzer, found := wm.analyzers[network.GetChainID()]
	if !found {
		analyzer = txanalyzer.NewGenericAnalyzer(reader, network)
		if err != nil {
			return err
		}
	}
	wm.analyzers[network.GetChainID()] = analyzer

	broadcaster, found := wm.broadcasters[network.GetChainID()]
	if !found {
		broadcaster, err = util.EthBroadcaster(network)
		if err != nil {
			return err
		}
	}
	wm.broadcasters[network.GetChainID()] = broadcaster

	txMonitor, found := wm.txMonitors[network.GetChainID()]
	if !found {
		txMonitor = monitor.NewGenericTxMonitor(reader)
	}
	wm.txMonitors[network.GetChainID()] = txMonitor

	return nil
}

func (wm *WalletManager) setPendingNonce(wallet common.Address, network networks.Network, nonce uint64) {
	wm.lock.Lock()
	defer wm.lock.Unlock()
	walletNonces := wm.pendingNonces[wallet]
	if walletNonces == nil {
		walletNonces = map[uint64]*big.Int{}
		wm.pendingNonces[wallet] = walletNonces
	}
	oldNonce := walletNonces[network.GetChainID()]
	if oldNonce != nil && oldNonce.Cmp(big.NewInt(int64(nonce))) >= 0 {
		return
	}
	walletNonces[network.GetChainID()] = big.NewInt(int64(nonce))
}

func (wm *WalletManager) pendingNonce(wallet common.Address, network networks.Network) *big.Int {
	wm.lock.RLock()
	defer wm.lock.RUnlock()
	walletPendingNonces := wm.pendingNonces[wallet]
	if walletPendingNonces == nil {
		return nil
	}
	result := walletPendingNonces[network.GetChainID()]
	if result != nil {
		// when there is a pending nonce, we add 1 to get the next nonce
		result = big.NewInt(0).Add(result, big.NewInt(1))
	}
	return result
}

//  1. get remote pending nonce
//  2. get local pending nonce
//  2. get mined nonce
//  3. if mined nonce == remote == local, all good, lets return the mined nonce
//  4. since mined nonce is always <= remote nonce, if mined nonce > local nonce,
//     this session doesn't catch up with mined txs (in case there are txs  that
//     were from other apps and they were mined), return max(mined none, remote nonce)
//     and set local nonce to max(mined none, remote nonce)
//  5. if not, means mined nonce is smaller than both remote and local pending nonce
//     5.1 if remote == local: means all pending txs are from this session, we return
//     local nonce
//     5.2 if remote > local: means there is pending txs from another app, we return
//     remote nonce in order not to mess up with the other txs, but give a warning
//     5.3 if local > remote: means txs from this session are not broadcasted to the
//     the notes, return local nonce and give warnings
func (wm *WalletManager) nonce(wallet common.Address, network networks.Network) (*big.Int, error) {
	reader := wm.Reader(network)
	minedNonce, err := reader.GetMinedNonce(wallet.Hex())
	if err != nil {
		return nil, fmt.Errorf("couldn't get mined nonce in context manager: %s", err)
	}

	remotePendingNonce, err := reader.GetPendingNonce(wallet.Hex())
	if err != nil {
		return nil, fmt.Errorf("couldn't get remote pending nonce in context manager: %s", err)
	}

	var localPendingNonce uint64
	localPendingNonceBig := wm.pendingNonce(wallet, network)

	if localPendingNonceBig == nil {
		wm.setPendingNonce(wallet, network, remotePendingNonce)
		localPendingNonce = remotePendingNonce
	} else {
		localPendingNonce = localPendingNonceBig.Uint64()
	}

	hasPendingTxsOnNodes := minedNonce < remotePendingNonce
	if !hasPendingTxsOnNodes {
		if minedNonce > remotePendingNonce {
			return nil, fmt.Errorf(
				"mined nonce is higher than pending nonce, this is abnormal data from nodes, retry again later",
			)
		}
		// in this case, minedNonce is supposed to == remotePendingNonce
		if localPendingNonce <= minedNonce {
			// in this case, minedNonce is more up to date, update localPendingNonce
			// and return minedNonce
			wm.setPendingNonce(wallet, network, minedNonce)
			return big.NewInt(int64(minedNonce)), nil
		} else {
			// in this case, local is more up to date, return pending nonce
			wm.setPendingNonce(wallet, network, localPendingNonce) // update local nonce to the latest
			return big.NewInt(int64(localPendingNonce)), nil
		}
	}

	if localPendingNonce <= minedNonce {
		// localPendingNonce <= minedNonce < remotePendingNonce
		// in this case, there are pending txs on nodes and they are
		// from other apps
		// TODO: put warnings
		// we don't have to update local pending nonce here since
		// it will be updated if the new tx is broadcasted with context manager
		wm.setPendingNonce(wallet, network, remotePendingNonce) // update local nonce to the latest
		return big.NewInt(int64(remotePendingNonce)), nil
	} else if localPendingNonce <= remotePendingNonce {
		// minedNonce < localPendingNonce <= remotePendingNonce
		// similar to the previous case, however, there are pending txs came from
		// jarvis as well. No need special treatments
		wm.setPendingNonce(wallet, network, remotePendingNonce) // update local nonce to the latest
		return big.NewInt(int64(remotePendingNonce)), nil
	}
	// minedNonce < remotePendingNonce < localPendingNonce
	// in this case, local has more pending txs, this is the case when
	// the node doesn't have full pending txs as local, something is
	// wrong with the local txs.
	// TODO: give warnings and check pending txs, see if they are not found and update
	// local pending nonce respectively and retry not found txs, need to figure out
	// a mechanism to stop trying as well.
	// For now, we will just go ahead with localPendingNonce
	wm.setPendingNonce(wallet, network, localPendingNonce) // update local nonce to the latest
	return big.NewInt(int64(localPendingNonce)), nil
}

func (wm *WalletManager) getGasSettingInfo(network networks.Network) *GasInfo {
	wm.lock.RLock()
	defer wm.lock.RUnlock()
	return wm.gasSettings[network.GetChainID()]
}

func (wm *WalletManager) setGasInfo(network networks.Network, info *GasInfo) {
	wm.lock.Lock()
	defer wm.lock.Unlock()
	wm.gasSettings[network.GetChainID()] = info
}

// implement a cache mechanism to be more efficient
func (wm *WalletManager) GasSetting(network networks.Network) (*GasInfo, error) {
	gasInfo := wm.getGasSettingInfo(network)
	if gasInfo == nil || time.Since(gasInfo.Timestamp) >= GAS_INFO_TTL {
		// gasInfo is not initiated or outdated
		reader := wm.Reader(network)
		gasPrice, gasTipCapGwei, err := reader.SuggestedGasSettings()
		if err != nil {
			return nil, fmt.Errorf("couldn't get gas settings in context manager: %w", err)
		}

		info := GasInfo{
			GasPrice:         gasPrice,
			BaseGasPrice:     nil,
			MaxPriorityPrice: gasTipCapGwei,
			FeePerGas:        gasPrice,
			Timestamp:        time.Now(),
		}
		wm.setGasInfo(network, &info)
		return &info, nil
	}
	return wm.getGasSettingInfo(network), nil
}

func (wm *WalletManager) BuildTx(
	txType uint8,
	from, to common.Address,
	nonce *big.Int,
	value *big.Int,
	gasLimit uint64,
	extraGasLimit uint64,
	gasBufferPercent float64,
	gasPrice float64,
	extraGasPrice float64,
	tipCapGwei float64,
	extraTipCapGwei float64,
	data []byte,
	network networks.Network,
) (tx *types.Transaction, err error) {
	if gasLimit == 0 {
		gasLimit, err = wm.Reader(network).EstimateExactGas(
			from.Hex(), to.Hex(),
			gasPrice,
			value,
			data,
		)
		if err != nil {
			return nil, errors.Join(
				ErrEstimateGasFailed,
				fmt.Errorf("couldn't estimate gas. The tx is meant to revert or network error. Detail: %w", err),
			)
		}
	}

	// Apply gas buffer percentage if specified
	// This is applied before extraGasLimit to allow combining both mechanisms
	if gasBufferPercent > 0 {
		bufferAmount := uint64(float64(gasLimit) * gasBufferPercent)
		gasLimit = gasLimit + bufferAmount
	}

	if nonce == nil {
		nonce, err = wm.nonce(from, network)
		if err != nil {
			return nil, errors.Join(
				ErrAcquireNonceFailed,
				fmt.Errorf("couldn't get nonce of the wallet from any nodes: %w", err),
			)
		}
	}

	if gasPrice == 0 {
		gasInfo, err := wm.GasSetting(network)
		if err != nil {
			return nil, errors.Join(
				ErrGetGasSettingFailed,
				fmt.Errorf("couldn't get gas price info from any nodes: %w", err),
			)
		}
		gasPrice = gasInfo.GasPrice
		tipCapGwei = gasInfo.MaxPriorityPrice
	}

	return jarviscommon.BuildExactTx(
		txType,
		nonce.Uint64(),
		to.Hex(),
		value,
		gasLimit+extraGasLimit,
		gasPrice+extraGasPrice,
		tipCapGwei+extraTipCapGwei,
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
	network, err := networks.GetNetworkByID(tx.ChainId().Uint64())
	// TODO: handle chainId 0 for old txs
	if err != nil {
		return "", false, BroadcastError(fmt.Errorf("tx is encoded with unsupported ChainID: %w", err))
	}
	hash, broadcasted, allErrors := wm.Broadcaster(network).BroadcastTx(tx)
	if broadcasted {
		err := wm.registerBroadcastedTx(tx, network)
		if err != nil {
			return "", false, BroadcastError(fmt.Errorf("couldn't register broadcasted tx in context manager: %w", err))
		}
	}
	return hash, broadcasted, NewBroadcastError(allErrors)
}

func (wm *WalletManager) BroadcastTxSync(
	tx *types.Transaction,
) (receipt *types.Receipt, err error) {
	network, err := networks.GetNetworkByID(tx.ChainId().Uint64())
	if err != nil {
		return nil, fmt.Errorf("tx is encoded with unsupported ChainID: %w", err)
	}
	receipt, err = wm.Broadcaster(network).BroadcastTxSync(tx)
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

type TxStatus struct {
	Status  string
	Receipt *types.Receipt
}

// MonitorTx non-blocking way to monitor the tx status, it returns a channel that will be closed when the tx monitoring is done
// the channel is supposed to receive the following values:
//  1. "mined" if the tx is mined
//  2. "slow" if the tx is too slow to be mined (so receiver might want to retry with higher gas price)
//  3. other strings if the tx failed and the reason is returned by the node or other debugging error message that the node can return
func (wm *WalletManager) MonitorTx(
	tx *types.Transaction,
	network networks.Network,
	txCheckInterval time.Duration,
) <-chan TxStatus {
	txMonitor := wm.getTxMonitor(network)
	statusChan := make(chan TxStatus)
	monitorChan := txMonitor.MakeWaitChannelWithInterval(tx.Hash().Hex(), txCheckInterval)
	go func() {
		select {
		case status := <-monitorChan:
			switch status.Status {
			case "done":
				statusChan <- TxStatus{
					Status:  "mined",
					Receipt: status.Receipt,
				}
			case "reverted":
				// TODO: analyze to see what is the reason
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
		close(statusChan)
	}()
	return statusChan
}

func (wm *WalletManager) getTxStatuses(
	oldTxs map[string]*types.Transaction,
	network networks.Network,
) (statuses map[string]jarviscommon.TxInfo, err error) {
	result := map[string]jarviscommon.TxInfo{}

	for _, tx := range oldTxs {
		txInfo, _ := wm.Reader(network).TxInfoFromHash(tx.Hash().Hex())
		result[tx.Hash().Hex()] = txInfo
	}

	return result, nil
}

// EnsureTx ensures the tx is broadcasted and mined, it will retry until the tx is mined
// it returns nil and error if:
//  1. the tx couldn't be built
//  2. the tx couldn't be broadcasted and get mined after 10 retries
//
// It always returns the tx that was mined, either if the tx was successful or reverted
// Possible errors:
//  1. ErrEstimateGasFailed
//  2. ErrAcquireNonceFailed
//  3. ErrGetGasSettingFailed
//  4. ErrEnsureTxOutOfRetries
//  5. ErrGasPriceLimitReached
//
// # If the caller wants to know the reason of the error, they can use errors.Is to check if the error is one of the above
//
// After building the tx and before signing and broadcasting, the caller can provide a function hook to receive the tx and building error,
// if the hook returns an error, the process will be stopped and the error will be returned. If the hook returns nil, the process will continue
// even if the tx building failed, in this case, it will retry with the same data up to 10 times and the hook will be called again.
//
// After signing and broadcasting successfully, the caller can provide a function hook to receive the signed tx and broadcast error,
// if the hook returns an error, the process will be stopped and the error will be returned. If the hook returns nil, the process will continue
// to monitor the tx to see if the tx is mined or not. If the tx is not mined, the process will retry either with a new nonce or with higher gas
// price and tip cap to ensure the tx is mined. Hooks will be called again in the retry process.
func (wm *WalletManager) EnsureTxWithHooks(
	numRetries int,
	sleepDuration time.Duration,
	txCheckInterval time.Duration,
	txType uint8,
	from, to common.Address,
	value *big.Int,
	gasLimit, extraGasLimit uint64, gasBufferPercent float64,
	gasPrice, extraGasPrice float64,
	tipCapGwei, extraTipCapGwei float64,
	maxGasPrice, maxTipCap float64,
	data []byte,
	network networks.Network,
	beforeSignAndBroadcastHook Hook,
	afterSignAndBroadcastHook Hook,
	abis []abi.ABI,
	gasEstimationFailedHook GasEstimationFailedHook,
) (tx *types.Transaction, receipt *types.Receipt, err error) {
	// Create execution context
	ctx, err := NewTxExecutionContext(
		numRetries, sleepDuration, txCheckInterval,
		txType, from, to, value,
		gasLimit, extraGasLimit, gasBufferPercent,
		gasPrice, extraGasPrice,
		tipCapGwei, extraTipCapGwei,
		maxGasPrice, maxTipCap,
		data, network,
		beforeSignAndBroadcastHook, afterSignAndBroadcastHook,
		abis, gasEstimationFailedHook,
	)
	if err != nil {
		return nil, nil, err
	}

	// Create error decoder
	errDecoder := wm.createErrorDecoder(abis)

	// Main execution loop
	for {
		// Only sleep after actual retry attempts, not slow monitoring
		if ctx.actualRetryCount > 0 {
			time.Sleep(ctx.sleepDuration)
		}

		// Execute transaction attempt
		result := wm.executeTransactionAttempt(ctx, errDecoder)

		if result.ShouldReturn {
			return result.Transaction, result.Receipt, result.Error
		}
		if result.ShouldRetry {
			continue
		}

		// Monitor and handle the transaction (only if we have a transaction to monitor)
		// in this case, result.Receipt can be filled already because of this rpc https://www.quicknode.com/docs/arbitrum/eth_sendRawTransactionSync
		if result.Transaction != nil && result.Receipt == nil {
			statusChan := wm.MonitorTx(result.Transaction, ctx.network, ctx.txCheckInterval)

			status := <-statusChan

			result = wm.handleTransactionStatus(status, result.Transaction, ctx)
			if result.ShouldReturn {
				return result.Transaction, result.Receipt, result.Error
			}
			if result.ShouldRetry {
				continue
			}
		}
	}
}

// executeTransactionAttempt handles building and broadcasting a single transaction attempt
func (wm *WalletManager) executeTransactionAttempt(
	ctx *TxExecutionContext,
	errDecoder *ErrorDecoder,
) *TxExecutionResult {
	// Build transaction
	builtTx, err := wm.BuildTx(
		ctx.txType,
		ctx.from,
		ctx.to,
		ctx.retryNonce,
		ctx.value,
		ctx.gasLimit,
		ctx.extraGasLimit,
		ctx.gasBufferPercent,
		ctx.retryGasPrice,
		ctx.extraGasPrice,
		ctx.retryTipCap,
		ctx.extraTipCapGwei,
		ctx.data,
		ctx.network,
	)

	// Handle gas estimation failure
	if errors.Is(err, ErrEstimateGasFailed) {
		return wm.handleGasEstimationFailure(ctx, errDecoder, err)
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
	_, err = wm.Reader(ctx.network).EthCall(ctx.from.Hex(), ctx.to.Hex(), ctx.data, nil)
	if err != nil {
		revertData, isRevert := ethclient.RevertErrorData(err)
		if isRevert {
			err = errors.Join(
				ErrSimulatedTxReverted,
				fmt.Errorf("tx will be reverted: %s. Detail: %w", string(revertData), err),
			)
			logger.WithFields(logger.Fields{
				"tx_hash":         builtTx.Hash().Hex(),
				"nonce":           builtTx.Nonce(),
				"gas_price":       builtTx.GasPrice().String(),
				"tip_cap":         builtTx.GasTipCap().String(),
				"max_fee_per_gas": builtTx.GasFeeCap().String(),
				"used_sync_tx":    ctx.network.IsSyncTxSupported(),
				"error":           err,
				"revert_data":     revertData,
			}).Debug("Tx simulation showed a revert error")
			// we need to persist a few calculated values here before retrying with the txs
			ctx.retryNonce = big.NewInt(int64(builtTx.Nonce()))
			ctx.gasLimit = builtTx.Gas()
			// we could persist the error here but then later we need to set it to nil, setting this to the error if the user is supposed to handle such error
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  true,
				ShouldReturn: false,
				Error:        nil,
			}
		} else {
			err = errors.Join(ErrSimulatedTxFailed, fmt.Errorf("couldn't simulate tx at pending state. Detail: %w", err))
			logger.WithFields(logger.Fields{
				"tx_hash":         builtTx.Hash().Hex(),
				"nonce":           builtTx.Nonce(),
				"gas_price":       builtTx.GasPrice().String(),
				"tip_cap":         builtTx.GasTipCap().String(),
				"max_fee_per_gas": builtTx.GasFeeCap().String(),
				"used_sync_tx":    ctx.network.IsSyncTxSupported(),
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
	result := wm.signAndBroadcastTransaction(builtTx, ctx)

	// If no transaction is set in result, use the built transaction
	if result.Transaction == nil && !result.ShouldReturn {
		result.Transaction = builtTx
	}

	return result
}

// handleGasEstimationFailure processes gas estimation failures and pending transaction checks
func (wm *WalletManager) handleGasEstimationFailure(
	ctx *TxExecutionContext,
	errDecoder *ErrorDecoder,
	err error,
) *TxExecutionResult {
	// Only check old transactions if we have any
	if len(ctx.oldTxs) > 0 {
		// Check if previous transactions might have been successful
		statuses, statusErr := wm.getTxStatuses(ctx.oldTxs, ctx.network)
		if statusErr != nil {
			logger.WithFields(logger.Fields{
				"error": statusErr,
			}).Debug("Getting tx statuses after gas estimation failure. Ignore and continue the retry loop")
			// Don't return immediately, proceed to hook handling
		} else {
			// Check for completed transactions
			for txhash, status := range statuses {
				if status.Status == "done" || status.Status == "reverted" {
					if tx, exists := ctx.oldTxs[txhash]; exists && tx != nil {
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
					if tx, exists := ctx.oldTxs[txhash]; exists && tx != nil {
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
	if errDecoder != nil && ctx.gasEstimationFailedHook != nil {
		abiError, revertParams, revertMsgErr := errDecoder.Decode(err)
		hookGasLimit, hookErr := ctx.gasEstimationFailedHook(nil, abiError, revertParams, revertMsgErr, err)
		if hookErr != nil {
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        hookErr,
			}
		}
		if hookGasLimit != nil {
			ctx.gasLimit = hookGasLimit.Uint64()
		}
	}

	// Increment retry count for gas estimation failure
	ctx.actualRetryCount++
	if ctx.actualRetryCount > ctx.numRetries {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error: errors.Join(
				ErrEnsureTxOutOfRetries,
				fmt.Errorf("gas estimation failed after %d retries", ctx.numRetries),
			),
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
func (wm *WalletManager) signAndBroadcastTransaction(
	tx *types.Transaction,
	ctx *TxExecutionContext,
) *TxExecutionResult {
	// Execute before hook
	if ctx.beforeSignAndBroadcastHook != nil {
		if hookError := ctx.beforeSignAndBroadcastHook(tx, nil); hookError != nil {
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error: fmt.Errorf(
					"after tx building and before signing and broadcasting hook error: %w",
					hookError,
				),
			}
		}
	}

	// Sign transaction
	signedAddr, signedTx, err := wm.SignTx(ctx.from, tx, ctx.network)
	if err != nil {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error:        fmt.Errorf("failed to sign transaction: %w", err),
		}
	}

	// Verify signed address matches expected address
	if signedAddr.Cmp(ctx.from) != 0 {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error: fmt.Errorf(
				"signed from wrong address. You could use wrong hw or passphrase. Expected wallet: %s, signed wallet: %s",
				ctx.from.Hex(),
				signedAddr.Hex(),
			),
		}
	}

	var receipt *types.Receipt
	var broadcastErr BroadcastError
	var successful bool

	// Broadcast transaction
	if ctx.network.IsSyncTxSupported() {
		receipt, broadcastErr = wm.BroadcastTxSync(signedTx)
		if receipt != nil {
			successful = true
		}
	} else {
		_, successful, broadcastErr = wm.BroadcastTx(signedTx)
	}

	if signedTx != nil {
		ctx.oldTxs[signedTx.Hash().Hex()] = signedTx
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
				"used_sync_tx":    ctx.network.IsSyncTxSupported(),
				"receipt":         receipt,
				"error":           broadcastErr,
			}).Debug("Unsuccessful signing and broadcasting transaction")
		} else {
			logger.WithFields(logger.Fields{
				"nonce":        tx.Nonce(),
				"gas_price":    tx.GasPrice().String(),
				"used_sync_tx": ctx.network.IsSyncTxSupported(),
				"receipt":      receipt,
				"error":        broadcastErr,
			}).Debug("Unsuccessful signing and broadcasting transaction (no signed tx)")
		}

		return wm.handleBroadcastError(broadcastErr, tx, ctx)
	}

	// Log successful broadcast
	logger.WithFields(logger.Fields{
		"tx_hash":         signedTx.Hash().Hex(),
		"nonce":           signedTx.Nonce(),
		"gas_price":       signedTx.GasPrice().String(),
		"tip_cap":         signedTx.GasTipCap().String(),
		"max_fee_per_gas": signedTx.GasFeeCap().String(),
		"used_sync_tx":    ctx.network.IsSyncTxSupported(),
		"receipt":         receipt,
	}).Info("Signed and broadcasted transaction")

	// Execute after hook - convert BroadcastError to error for hook
	var hookErr error
	if broadcastErr != nil {
		hookErr = broadcastErr
	}

	if ctx.afterSignAndBroadcastHook != nil {
		if hookError := ctx.afterSignAndBroadcastHook(signedTx, hookErr); hookError != nil {
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
func (wm *WalletManager) handleBroadcastError(
	broadcastErr BroadcastError,
	tx *types.Transaction,
	ctx *TxExecutionContext,
) *TxExecutionResult {
	// Special case: nonce is low requires checking if transaction is already mined
	if broadcastErr == ErrNonceIsLow {
		return wm.handleNonceIsLowError(tx, ctx)
	}

	// Special case: tx is known doesn't count as retry (we're just waiting for it to be mined)
	// in this case, we need to speed up the tx by increasing the gas price and tip cap
	// however, it should be handled by the slow status gotten from the monitor tx
	// so we just need to retry with the same nonce
	if broadcastErr == ErrTxIsKnown {
		ctx.retryNonce = big.NewInt(int64(tx.Nonce()))
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

	if result := ctx.incrementRetryCountAndCheck(errorMsg); result != nil {
		return result
	}

	// Keep the same nonce for retry
	ctx.retryNonce = big.NewInt(int64(tx.Nonce()))
	return &TxExecutionResult{
		Transaction:  nil,
		ShouldRetry:  true,
		ShouldReturn: false,
		Error:        nil,
	}
}

// handleNonceIsLowError specifically handles the nonce is low error case
func (wm *WalletManager) handleNonceIsLowError(tx *types.Transaction, ctx *TxExecutionContext) *TxExecutionResult {
	statuses, err := wm.getTxStatuses(ctx.oldTxs, ctx.network)
	if err != nil {
		logger.WithFields(logger.Fields{
			"error": err,
		}).Debug("Getting tx statuses in case where tx wasn't broadcasted because nonce is too low. Ignore and continue the retry loop")

		if result := ctx.incrementRetryCountAndCheck("nonce is low and status check failed"); result != nil {
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
			if tx, exists := ctx.oldTxs[txhash]; exists && tx != nil {
				return &TxExecutionResult{
					Transaction:  tx,
					ShouldRetry:  false,
					ShouldReturn: true,
					Error:        nil,
				}
			}
		}
	}

	// No completed transactions found, retry with new nonce
	if result := ctx.incrementRetryCountAndCheck("nonce is low and no pending transactions"); result != nil {
		return result
	}

	ctx.retryNonce = nil

	return &TxExecutionResult{
		Transaction:  nil,
		ShouldRetry:  true,
		ShouldReturn: false,
		Error:        nil,
	}
}

// incrementRetryCountAndCheck increments retry count and checks if we've exceeded retries
func (ctx *TxExecutionContext) incrementRetryCountAndCheck(errorMsg string) *TxExecutionResult {
	ctx.actualRetryCount++
	if ctx.actualRetryCount > ctx.numRetries {
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  false,
			ShouldReturn: true,
			Error: errors.Join(
				ErrEnsureTxOutOfRetries,
				fmt.Errorf("%s after %d retries", errorMsg, ctx.numRetries),
			),
		}
	}
	return nil
}

// handleTransactionStatus processes different transaction statuses
func (wm *WalletManager) handleTransactionStatus(
	status TxStatus,
	signedTx *types.Transaction,
	ctx *TxExecutionContext,
) *TxExecutionResult {
	switch status.Status {
	case "mined", "reverted":
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

		ctx.actualRetryCount++
		if ctx.actualRetryCount > ctx.numRetries {
			return &TxExecutionResult{
				Transaction:  nil,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error: errors.Join(
					ErrEnsureTxOutOfRetries,
					fmt.Errorf("transaction lost after %d retries", ctx.numRetries),
				),
			}
		}
		ctx.retryNonce = nil

		// Sleep will be handled in main loop based on actualRetryCount
		return &TxExecutionResult{
			Transaction:  nil,
			ShouldRetry:  true,
			ShouldReturn: false,
			Error:        nil,
		}

	case "slow":
		// Try to adjust gas prices for slow transaction
		if ctx.adjustGasPricesForSlowTx(signedTx) {
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
				"max_gas_price": ctx.maxGasPrice,
				"max_tip_cap":   ctx.maxTipCap,
			}).Warn("Transaction slow but gas price protection limits reached. Stopping retry attempts.")

			return &TxExecutionResult{
				Transaction:  signedTx,
				ShouldRetry:  false,
				ShouldReturn: true,
				Error:        errors.Join(ErrGasPriceLimitReached, fmt.Errorf("maxGasPrice: %f, maxTipCap: %f", ctx.maxGasPrice, ctx.maxTipCap)),
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
		gasLimit, extraGasLimit, 0, // 0 means no gas buffer
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
