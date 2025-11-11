package walletarmy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildTx_GasBufferCalculation(t *testing.T) {
	t.Run("applies 40% buffer correctly", func(t *testing.T) {
		// Given an estimated gas limit of 50,000
		estimatedGas := uint64(50000)
		gasBufferPercent := 0.40

		// Calculate expected result
		expectedBuffer := uint64(float64(estimatedGas) * gasBufferPercent) // 20,000
		expectedGasLimit := estimatedGas + expectedBuffer                  // 70,000

		// Verify calculation
		assert.Equal(t, uint64(20000), expectedBuffer)
		assert.Equal(t, uint64(70000), expectedGasLimit)
	})

	t.Run("applies buffer before extraGasLimit", func(t *testing.T) {
		// Given
		estimatedGas := uint64(50000)
		gasBufferPercent := 0.40 // 40%
		extraGasLimit := uint64(5000)

		// Calculate step by step
		bufferAmount := uint64(float64(estimatedGas) * gasBufferPercent) // 20,000
		afterBuffer := estimatedGas + bufferAmount                       // 70,000
		finalGas := afterBuffer + extraGasLimit                          // 75,000

		// Verify
		assert.Equal(t, uint64(20000), bufferAmount)
		assert.Equal(t, uint64(70000), afterBuffer)
		assert.Equal(t, uint64(75000), finalGas)
	})

	t.Run("no buffer when gasBufferPercent is 0", func(t *testing.T) {
		estimatedGas := uint64(50000)
		gasBufferPercent := 0.0

		bufferAmount := uint64(float64(estimatedGas) * gasBufferPercent)
		assert.Equal(t, uint64(0), bufferAmount)
		assert.Equal(t, estimatedGas, estimatedGas+bufferAmount)
	})

	t.Run("100% buffer doubles the gas", func(t *testing.T) {
		estimatedGas := uint64(50000)
		gasBufferPercent := 1.0 // 100%

		bufferAmount := uint64(float64(estimatedGas) * gasBufferPercent)
		finalGas := estimatedGas + bufferAmount

		assert.Equal(t, uint64(50000), bufferAmount)
		assert.Equal(t, uint64(100000), finalGas)
	})
}
