package walletarmy

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/tranvictor/jarvis/networks"
)

// MockHook for testing hooks
func mockHook(tx *types.Transaction, err error) error {
	return nil
}

// MockGasEstimationFailedHook for testing gas estimation failed hooks
func mockGasEstimationFailedHook(
	tx *types.Transaction,
	abiError *abi.Error,
	revertParams any,
	revertMsgError, gasEstimationError error,
) (gasLimit *big.Int, err error) {
	return big.NewInt(21000), nil
}

func TestWalletManager_R(t *testing.T) {
	wm := &WalletManager{}

	req := wm.R()

	assert.NotNil(t, req)
	assert.Equal(t, wm, req.wm)
	assert.Equal(t, big.NewInt(0), req.value)
	assert.Equal(t, 0, req.numRetries)
	assert.Equal(t, time.Duration(0), req.sleepDuration)
	assert.Equal(t, uint8(0), req.txType)
	assert.Equal(t, common.Address{}, req.from)
	assert.Equal(t, common.Address{}, req.to)
	assert.Equal(t, uint64(0), req.gasLimit)
	assert.Equal(t, uint64(0), req.extraGasLimit)
	assert.Equal(t, float64(0), req.gasPrice)
	assert.Equal(t, float64(0), req.extraGasPrice)
	assert.Equal(t, float64(0), req.tipCapGwei)
	assert.Equal(t, float64(0), req.extraTipCapGwei)
	assert.Nil(t, req.data)
	assert.Equal(t, networks.EthereumMainnet, req.network)
	assert.Nil(t, req.beforeSignAndBroadcastHook)
	assert.Nil(t, req.afterSignAndBroadcastHook)
	assert.Nil(t, req.abis)
	assert.Nil(t, req.gasEstimationFailedHook)
}

func TestTxRequest_SetNumRetries(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetNumRetries(5)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, 5, req.numRetries)
}

func TestTxRequest_SetSleepDuration(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()
	duration := 2 * time.Second

	result := req.SetSleepDuration(duration)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, duration, req.sleepDuration)
}

func TestTxRequest_SetTxType(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetTxType(2)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, uint8(2), req.txType)
}

func TestTxRequest_SetFrom(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()
	fromAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")

	result := req.SetFrom(fromAddr)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, fromAddr, req.from)
}

func TestTxRequest_SetTo(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()
	toAddr := common.HexToAddress("0x0987654321098765432109876543210987654321")

	result := req.SetTo(toAddr)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, toAddr, req.to)
}

func TestTxRequest_SetValue(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	t.Run("with valid value", func(t *testing.T) {
		value := big.NewInt(1000000000000000000) // 1 ETH in wei
		result := req.SetValue(value)

		assert.Equal(t, req, result) // Should return self for chaining
		assert.Equal(t, value, req.value)
	})

	t.Run("with nil value", func(t *testing.T) {
		originalValue := req.value
		result := req.SetValue(nil)

		assert.Equal(t, req, result)              // Should return self for chaining
		assert.Equal(t, originalValue, req.value) // Should not change when nil
	})
}

func TestTxRequest_SetGasLimit(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetGasLimit(21000)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, uint64(21000), req.gasLimit)
}

func TestTxRequest_SetExtraGasLimit(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetExtraGasLimit(5000)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, uint64(5000), req.extraGasLimit)
}

func TestTxRequest_SetGasPrice(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetGasPrice(20.5)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, 20.5, req.gasPrice)
}

func TestTxRequest_SetExtraGasPrice(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetExtraGasPrice(5.5)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, 5.5, req.extraGasPrice)
}

func TestTxRequest_SetTipCapGwei(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetTipCapGwei(2.0)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, 2.0, req.tipCapGwei)
}

func TestTxRequest_SetExtraTipCapGwei(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetExtraTipCapGwei(1.5)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, 1.5, req.extraTipCapGwei)
}

func TestTxRequest_SetGasBufferPercent(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	t.Run("with valid percentage", func(t *testing.T) {
		result := req.SetGasBufferPercent(0.40)

		assert.Equal(t, req, result) // Should return self for chaining
		assert.Equal(t, 0.40, req.gasBufferPercent)
	})

	t.Run("with zero percentage", func(t *testing.T) {
		req := wm.R()
		result := req.SetGasBufferPercent(0)

		assert.Equal(t, req, result)
		assert.Equal(t, 0.0, req.gasBufferPercent)
	})

	t.Run("with negative percentage", func(t *testing.T) {
		req := wm.R()
		result := req.SetGasBufferPercent(-0.10)

		assert.Equal(t, req, result)
		assert.Equal(t, -0.10, req.gasBufferPercent)
	})

	t.Run("with large percentage", func(t *testing.T) {
		req := wm.R()
		result := req.SetGasBufferPercent(1.0) // 100% buffer

		assert.Equal(t, req, result)
		assert.Equal(t, 1.0, req.gasBufferPercent)
	})

	t.Run("overwriting previous value", func(t *testing.T) {
		req := wm.R()
		req.SetGasBufferPercent(0.20)
		assert.Equal(t, 0.20, req.gasBufferPercent)

		result := req.SetGasBufferPercent(0.50)
		assert.Equal(t, req, result)
		assert.Equal(t, 0.50, req.gasBufferPercent)
	})
}

func TestTxRequest_GasBufferInBuilderChain(t *testing.T) {
	wm := &WalletManager{}
	fromAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	toAddr := common.HexToAddress("0x0987654321098765432109876543210987654321")

	// Test chaining with gas buffer
	req := wm.R().
		SetFrom(fromAddr).
		SetTo(toAddr).
		SetGasLimit(0).
		SetGasBufferPercent(0.40).
		SetExtraGasLimit(5000)

	assert.Equal(t, fromAddr, req.from)
	assert.Equal(t, toAddr, req.to)
	assert.Equal(t, uint64(0), req.gasLimit)
	assert.Equal(t, 0.40, req.gasBufferPercent)
	assert.Equal(t, uint64(5000), req.extraGasLimit)
}

func TestTxRequest_SetData(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()
	data := []byte{0x01, 0x02, 0x03, 0x04}

	result := req.SetData(data)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, data, req.data)
}

func TestTxRequest_SetNetwork(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()
	network := networks.EthereumMainnet

	result := req.SetNetwork(network)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.Equal(t, network, req.network)
}

func TestTxRequest_SetBeforeSignAndBroadcastHook(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetBeforeSignAndBroadcastHook(mockHook)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.NotNil(t, req.beforeSignAndBroadcastHook)
}

func TestTxRequest_SetAfterSignAndBroadcastHook(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	result := req.SetAfterSignAndBroadcastHook(mockHook)

	assert.Equal(t, req, result) // Should return self for chaining
	assert.NotNil(t, req.afterSignAndBroadcastHook)
}

func TestTxRequest_SetAbis(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	t.Run("with single ABI", func(t *testing.T) {
		// Create a simple mock ABI
		mockABI := abi.ABI{}
		result := req.SetAbis(mockABI)

		assert.Equal(t, req, result) // Should return self for chaining
		assert.Equal(t, 1, len(req.abis))
		assert.Equal(t, mockABI, req.abis[0])
	})

	t.Run("with multiple ABIs", func(t *testing.T) {
		req := wm.R() // Fresh request
		mockABI1 := abi.ABI{}
		mockABI2 := abi.ABI{}
		mockABI3 := abi.ABI{}

		result := req.SetAbis(mockABI1, mockABI2, mockABI3)

		assert.Equal(t, req, result) // Should return self for chaining
		assert.Equal(t, 3, len(req.abis))
		assert.Equal(t, mockABI1, req.abis[0])
		assert.Equal(t, mockABI2, req.abis[1])
		assert.Equal(t, mockABI3, req.abis[2])
	})

	t.Run("with no ABIs", func(t *testing.T) {
		req := wm.R() // Fresh request
		result := req.SetAbis()

		assert.Equal(t, req, result) // Should return self for chaining
		assert.Equal(t, 0, len(req.abis))
	})

	t.Run("overwriting previous ABIs", func(t *testing.T) {
		req := wm.R() // Fresh request
		mockABI1 := abi.ABI{}
		mockABI2 := abi.ABI{}

		// Set initial ABIs
		req.SetAbis(mockABI1)
		assert.Equal(t, 1, len(req.abis))

		// Overwrite with new ABIs
		result := req.SetAbis(mockABI2)

		assert.Equal(t, req, result) // Should return self for chaining
		assert.Equal(t, 1, len(req.abis))
		assert.Equal(t, mockABI2, req.abis[0])
	})
}

func TestTxRequest_SetGasEstimationFailedHook(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	t.Run("with valid hook", func(t *testing.T) {
		result := req.SetGasEstimationFailedHook(mockGasEstimationFailedHook)

		assert.Equal(t, req, result) // Should return self for chaining
		assert.NotNil(t, req.gasEstimationFailedHook)
	})

	t.Run("with nil hook", func(t *testing.T) {
		req := wm.R() // Fresh request
		result := req.SetGasEstimationFailedHook(nil)

		assert.Equal(t, req, result) // Should return self for chaining
		assert.Nil(t, req.gasEstimationFailedHook)
	})

	t.Run("overwriting previous hook", func(t *testing.T) {
		req := wm.R() // Fresh request

		// Create two different hook functions
		hook1 := func(tx *types.Transaction, abiError *abi.Error, revertParams any, revertMsgError, gasEstimationError error) (gasLimit *big.Int, err error) {
			return big.NewInt(25000), nil
		}
		hook2 := func(tx *types.Transaction, abiError *abi.Error, revertParams any, revertMsgError, gasEstimationError error) (gasLimit *big.Int, err error) {
			return big.NewInt(30000), nil
		}

		// Set initial hook
		req.SetGasEstimationFailedHook(hook1)
		assert.NotNil(t, req.gasEstimationFailedHook)

		// Overwrite with new hook
		result := req.SetGasEstimationFailedHook(hook2)

		assert.Equal(t, req, result) // Should return self for chaining
		assert.NotNil(t, req.gasEstimationFailedHook)
	})
}

func TestGasEstimationFailedHook_Functionality(t *testing.T) {
	t.Run("hook returns gas limit and no error", func(t *testing.T) {
		hook := func(tx *types.Transaction, revertMsgError, gasEstimationError error) (gasLimit *big.Int, err error) {
			return big.NewInt(50000), nil
		}

		// Create a dummy transaction
		tx := types.NewTransaction(0, common.Address{}, big.NewInt(0), 21000, big.NewInt(1000000000), nil)

		gasLimit, err := hook(tx, nil, nil)

		assert.NoError(t, err)
		assert.Equal(t, big.NewInt(50000), gasLimit)
	})

	t.Run("hook returns error to stop execution", func(t *testing.T) {
		expectedErr := assert.AnError
		hook := func(tx *types.Transaction, revertMsgError, gasEstimationError error) (gasLimit *big.Int, err error) {
			return nil, expectedErr
		}

		// Create a dummy transaction
		tx := types.NewTransaction(0, common.Address{}, big.NewInt(0), 21000, big.NewInt(1000000000), nil)

		gasLimit, err := hook(tx, nil, nil)

		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, gasLimit)
	})

	t.Run("hook handles revert message and gas estimation errors", func(t *testing.T) {
		revertErr := assert.AnError
		gasEstErr := assert.AnError

		hook := func(tx *types.Transaction, revertMsgError, gasEstimationError error) (gasLimit *big.Int, err error) {
			assert.Equal(t, revertErr, revertMsgError)
			assert.Equal(t, gasEstErr, gasEstimationError)
			return big.NewInt(40000), nil
		}

		// Create a dummy transaction
		tx := types.NewTransaction(0, common.Address{}, big.NewInt(0), 21000, big.NewInt(1000000000), nil)

		gasLimit, err := hook(tx, revertErr, gasEstErr)

		assert.NoError(t, err)
		assert.Equal(t, big.NewInt(40000), gasLimit)
	})
}

func TestTxRequest_BuilderPatternChaining(t *testing.T) {
	wm := &WalletManager{}
	fromAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	toAddr := common.HexToAddress("0x0987654321098765432109876543210987654321")
	value := big.NewInt(1000000000000000000) // 1 ETH in wei
	data := []byte{0x01, 0x02, 0x03, 0x04}
	network := networks.EthereumMainnet
	duration := 2 * time.Second
	mockABI := abi.ABI{}

	// Test chaining multiple methods together including gas buffer
	req := wm.R().
		SetNumRetries(3).
		SetSleepDuration(duration).
		SetTxType(2).
		SetFrom(fromAddr).
		SetTo(toAddr).
		SetValue(value).
		SetGasLimit(21000).
		SetExtraGasLimit(1000).
		SetGasBufferPercent(0.40).
		SetGasPrice(20.5).
		SetExtraGasPrice(5.0).
		SetTipCapGwei(2.0).
		SetExtraTipCapGwei(1.0).
		SetData(data).
		SetNetwork(network).
		SetBeforeSignAndBroadcastHook(mockHook).
		SetAfterSignAndBroadcastHook(mockHook).
		SetAbis(mockABI).
		SetGasEstimationFailedHook(mockGasEstimationFailedHook)

	// Verify all values were set correctly including gas buffer
	assert.Equal(t, 3, req.numRetries)
	assert.Equal(t, duration, req.sleepDuration)
	assert.Equal(t, uint8(2), req.txType)
	assert.Equal(t, fromAddr, req.from)
	assert.Equal(t, toAddr, req.to)
	assert.Equal(t, value, req.value)
	assert.Equal(t, uint64(21000), req.gasLimit)
	assert.Equal(t, uint64(1000), req.extraGasLimit)
	assert.Equal(t, 0.40, req.gasBufferPercent)
	assert.Equal(t, 20.5, req.gasPrice)
	assert.Equal(t, 5.0, req.extraGasPrice)
	assert.Equal(t, 2.0, req.tipCapGwei)
	assert.Equal(t, 1.0, req.extraTipCapGwei)
	assert.Equal(t, data, req.data)
	assert.Equal(t, network, req.network)
	assert.NotNil(t, req.beforeSignAndBroadcastHook)
	assert.NotNil(t, req.afterSignAndBroadcastHook)
	assert.Equal(t, 1, len(req.abis))
	assert.Equal(t, mockABI, req.abis[0])
	assert.NotNil(t, req.gasEstimationFailedHook)
}

func TestTxRequest_MultipleSettersOfSameType(t *testing.T) {
	wm := &WalletManager{}
	req := wm.R()

	// Test that calling the same setter multiple times overwrites the previous value
	req.SetNumRetries(5).SetNumRetries(10).SetNumRetries(15)
	assert.Equal(t, 15, req.numRetries)

	req.SetGasPrice(10.0).SetGasPrice(20.0).SetGasPrice(30.0)
	assert.Equal(t, 30.0, req.gasPrice)

	value1 := big.NewInt(100)
	value2 := big.NewInt(200)
	value3 := big.NewInt(300)
	req.SetValue(value1).SetValue(value2).SetValue(value3)
	assert.Equal(t, value3, req.value)

	// Test multiple ABI sets
	mockABI1 := abi.ABI{}
	mockABI2 := abi.ABI{}
	req.SetAbis(mockABI1).SetAbis(mockABI2)
	assert.Equal(t, 1, len(req.abis))
	assert.Equal(t, mockABI2, req.abis[0])

	// Test multiple gas estimation failed hook sets
	hook1 := func(tx *types.Transaction, abiError *abi.Error, revertParams any, revertMsgError, gasEstimationError error) (gasLimit *big.Int, err error) {
		return big.NewInt(25000), nil
	}
	hook2 := func(tx *types.Transaction, abiError *abi.Error, revertParams any, revertMsgError, gasEstimationError error) (gasLimit *big.Int, err error) {
		return big.NewInt(35000), nil
	}
	req.SetGasEstimationFailedHook(hook1).SetGasEstimationFailedHook(hook2)
	assert.NotNil(t, req.gasEstimationFailedHook)
}
