package walletarmy

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/tranvictor/jarvis/networks"
)

func TestTxExecutionContext_adjustGasPricesForSlowTx(t *testing.T) {
	ctx := &TxExecutionContext{
		extraGasPrice:   10.0,
		extraTipCapGwei: 5.0,
		maxGasPrice:     0, // No limit
		maxTipCap:       0, // No limit
	}

	// Test with nil transaction
	result := ctx.adjustGasPricesForSlowTx(nil)
	if result != false {
		t.Error("adjustGasPricesForSlowTx should return false for nil transaction")
	}
	if ctx.retryGasPrice != 0 || ctx.retryTipCap != 0 {
		t.Error("adjustGasPricesForSlowTx should not modify values when tx is nil")
	}

	// Test with valid transaction
	gasPrice := big.NewInt(100000000000) // 100 Gwei
	tipCap := big.NewInt(50000000000)    // 50 Gwei
	tx := types.NewTx(&types.DynamicFeeTx{
		GasFeeCap: gasPrice,
		GasTipCap: tipCap,
		Nonce:     5,
	})

	result = ctx.adjustGasPricesForSlowTx(tx)
	if result != true {
		t.Error("adjustGasPricesForSlowTx should return true for successful adjustment")
	}

	expectedGasPrice := 100.0 * GasPriceIncreasePercent // (100 * 1.2) = 120
	expectedTipCap := 50.0 * TipCapIncreasePercent      // (50 * 1.1) = 55

	const epsilon = 0.0001
	if diff := ctx.retryGasPrice - expectedGasPrice; diff < -epsilon || diff > epsilon {
		t.Errorf("Expected retry gas price %f, got %f", expectedGasPrice, ctx.retryGasPrice)
	}

	if diff := ctx.retryTipCap - expectedTipCap; diff < -epsilon || diff > epsilon {
		t.Errorf("Expected retry tip cap %f, got %f", expectedTipCap, ctx.retryTipCap)
	}

	if ctx.retryNonce.Cmp(big.NewInt(5)) != 0 {
		t.Errorf("Expected retry nonce 5, got %s", ctx.retryNonce.String())
	}

	// Test with gas price limit that allows adjustment
	ctxWithLimit := &TxExecutionContext{
		extraGasPrice:   10.0,
		extraTipCapGwei: 5.0,
		maxGasPrice:     130.0, // Set a limit higher than adjusted price (120)
		maxTipCap:       0,     // No tip cap limit
	}

	result = ctxWithLimit.adjustGasPricesForSlowTx(tx)
	if result != true {
		t.Error("Expected adjustment to succeed when below gas price limit")
	}

	// This should fail due to gas price limit reached
	ctxWithLowLimit := &TxExecutionContext{
		extraGasPrice:   10.0,
		extraTipCapGwei: 5.0,
		maxGasPrice:     115.0, // Lower than 120 (100 * 1.2)
		maxTipCap:       0,     // No tip cap limit
	}

	result = ctxWithLowLimit.adjustGasPricesForSlowTx(tx)
	if result != false {
		t.Error("Expected adjustment to fail when gas price limit reached")
	}
}

func TestNewTxExecutionContext_Validation(t *testing.T) {
	// Use mainnet for testing
	network, err := networks.GetNetwork("mainnet")
	if err != nil {
		t.Fatalf("Failed to get mainnet network: %v", err)
	}
	from := common.HexToAddress("0x1234567890123456789012345678901234567890")
	to := common.HexToAddress("0x0987654321098765432109876543210987654321")

	// Test with negative retries - should be set to 0
	ctx, err := NewTxExecutionContext(
		-1, 0, 0,
		0, from, to, nil,
		0, 0, 0.0, 0, 0, 0, 0,
		0, 0, // maxGasPrice, maxTipCap
		nil, network,
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if ctx.numRetries != 0 {
		t.Errorf("Expected retries to be 0 when negative value passed, got %d", ctx.numRetries)
	}

	if ctx.sleepDuration != DefaultSleepDuration {
		t.Errorf("Expected default sleep duration %v, got %v", DefaultSleepDuration, ctx.sleepDuration)
	}

	if ctx.txCheckInterval != DefaultTxCheckInterval {
		t.Errorf("Expected default tx check interval %v, got %v", DefaultTxCheckInterval, ctx.txCheckInterval)
	}

	if ctx.value == nil {
		t.Error("Value should be initialized to zero when nil")
	}

	// Test error conditions
	t.Run("zero from address", func(t *testing.T) {
		_, err := NewTxExecutionContext(
			1, time.Second, time.Second,
			0, common.Address{}, to, nil,
			0, 0, 0.0, 0, 0, 0, 0,
			0, 0, // maxGasPrice, maxTipCap
			nil, network,
			nil, nil, nil, nil,
		)
		if err != ErrFromAddressZero {
			t.Errorf("Expected ErrFromAddressZero, got %v", err)
		}
	})

	t.Run("nil network", func(t *testing.T) {
		_, err := NewTxExecutionContext(
			1, time.Second, time.Second,
			0, from, to, nil,
			0, 0, 0.0, 0, 0, 0, 0,
			0, 0, // maxGasPrice, maxTipCap
			nil, nil,
			nil, nil, nil, nil,
		)
		if err != ErrNetworkNil {
			t.Errorf("Expected ErrNetworkNil, got %v", err)
		}
	})
}
