package walletarmy

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/tranvictor/jarvis/networks"
)

// TxRequest represents a transaction request with builder pattern
type TxRequest struct {
	wm *WalletManager

	numRetries      int
	sleepDuration   time.Duration
	txCheckInterval time.Duration

	// Transaction parameters
	txType           uint8
	from, to         common.Address
	value            *big.Int
	gasLimit         uint64
	extraGasLimit    uint64
	gasBufferPercent float64
	gasPrice         float64
	extraGasPrice    float64
	tipCapGwei       float64
	extraTipCapGwei  float64
	maxGasPrice      float64
	maxTipCap        float64
	data             []byte
	network          networks.Network

	// Hooks
	beforeSignAndBroadcastHook Hook
	afterSignAndBroadcastHook  Hook
	gasEstimationFailedHook    GasEstimationFailedHook
	abis                       []abi.ABI
}

// R creates a new transaction request (similar to go-resty's R() method)
func (wm *WalletManager) R() *TxRequest {
	return &TxRequest{
		wm:      wm,
		value:   big.NewInt(0),            // default value
		network: networks.EthereumMainnet, // default network is Ethereum Mainnet
	}
}

// SetNumRetries sets the number of retries
func (r *TxRequest) SetNumRetries(numRetries int) *TxRequest {
	r.numRetries = numRetries
	return r
}

// SetSleepDuration sets the sleep duration
func (r *TxRequest) SetSleepDuration(sleepDuration time.Duration) *TxRequest {
	r.sleepDuration = sleepDuration
	return r
}

// SetTxCheckInterval sets the transaction check interval
func (r *TxRequest) SetTxCheckInterval(txCheckInterval time.Duration) *TxRequest {
	r.txCheckInterval = txCheckInterval
	return r
}

// SetTxType sets the transaction type
func (r *TxRequest) SetTxType(txType uint8) *TxRequest {
	r.txType = txType
	return r
}

// SetFrom sets the from address
func (r *TxRequest) SetFrom(from common.Address) *TxRequest {
	r.from = from
	return r
}

// SetTo sets the to address
func (r *TxRequest) SetTo(to common.Address) *TxRequest {
	r.to = to
	return r
}

// SetValue sets the transaction value
func (r *TxRequest) SetValue(value *big.Int) *TxRequest {
	if value != nil {
		r.value = value
	}
	return r
}

// SetGasLimit sets the gas limit
func (r *TxRequest) SetGasLimit(gasLimit uint64) *TxRequest {
	r.gasLimit = gasLimit
	return r
}

// SetExtraGasLimit sets the extra gas limit
func (r *TxRequest) SetExtraGasLimit(extraGasLimit uint64) *TxRequest {
	r.extraGasLimit = extraGasLimit
	return r
}

// SetGasBufferPercent sets the gas buffer percentage
// The buffer is applied as a percentage of the estimated/provided gas limit
// For example, 0.40 means 40% buffer will be added to the gas limit
// The final gas limit will be: gasLimit + (gasLimit * gasBufferPercent) + extraGasLimit
// Note: Buffer is only applied when gasBufferPercent > 0; zero or negative values are ignored
func (r *TxRequest) SetGasBufferPercent(gasBufferPercent float64) *TxRequest {
	r.gasBufferPercent = gasBufferPercent
	return r
}

// SetGasPrice sets the gas price
func (r *TxRequest) SetGasPrice(gasPrice float64) *TxRequest {
	r.gasPrice = gasPrice
	return r
}

// SetExtraGasPrice sets the extra gas price
func (r *TxRequest) SetExtraGasPrice(extraGasPrice float64) *TxRequest {
	r.extraGasPrice = extraGasPrice
	return r
}

// SetTipCapGwei sets the tip cap in gwei
func (r *TxRequest) SetTipCapGwei(tipCapGwei float64) *TxRequest {
	r.tipCapGwei = tipCapGwei
	return r
}

// SetExtraTipCapGwei sets the extra tip cap in gwei
func (r *TxRequest) SetExtraTipCapGwei(extraTipCapGwei float64) *TxRequest {
	r.extraTipCapGwei = extraTipCapGwei
	return r
}

// SetMaxGasPrice sets the maximum gas price limit
func (r *TxRequest) SetMaxGasPrice(maxGasPrice float64) *TxRequest {
	r.maxGasPrice = maxGasPrice
	return r
}

// SetMaxTipCap sets the maximum tip cap limit
func (r *TxRequest) SetMaxTipCap(maxTipCap float64) *TxRequest {
	r.maxTipCap = maxTipCap
	return r
}

// SetData sets the transaction data
func (r *TxRequest) SetData(data []byte) *TxRequest {
	r.data = data
	return r
}

// SetNetwork sets the network
func (r *TxRequest) SetNetwork(network networks.Network) *TxRequest {
	r.network = network
	return r
}

// SetAbis sets the abis
func (r *TxRequest) SetAbis(abis ...abi.ABI) *TxRequest {
	r.abis = abis
	return r
}

// SetBeforeSignAndBroadcastHook sets the hook to be called before signing and broadcasting
func (r *TxRequest) SetBeforeSignAndBroadcastHook(hook Hook) *TxRequest {
	r.beforeSignAndBroadcastHook = hook
	return r
}

// SetAfterSignAndBroadcastHook sets the hook to be called after signing and broadcasting
func (r *TxRequest) SetAfterSignAndBroadcastHook(hook Hook) *TxRequest {
	r.afterSignAndBroadcastHook = hook
	return r
}

// SetGasEstimationFailedHook sets the hook to be called when gas estimation fails
func (r *TxRequest) SetGasEstimationFailedHook(hook GasEstimationFailedHook) *TxRequest {
	r.gasEstimationFailedHook = hook
	return r
}

// Execute executes the transaction request
func (r *TxRequest) Execute() (*types.Transaction, *types.Receipt, error) {
	return r.wm.EnsureTxWithHooks(
		r.numRetries,
		r.sleepDuration,
		r.txCheckInterval,
		r.txType,
		r.from,
		r.to,
		r.value,
		r.gasLimit, r.extraGasLimit, r.gasBufferPercent,
		r.gasPrice, r.extraGasPrice,
		r.tipCapGwei, r.extraTipCapGwei,
		r.maxGasPrice, r.maxTipCap,
		r.data,
		r.network,
		r.beforeSignAndBroadcastHook,
		r.afterSignAndBroadcastHook,
		r.abis,
		r.gasEstimationFailedHook,
	)
}
