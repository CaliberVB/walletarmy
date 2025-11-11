# wallet-army

A Go library for managing Ethereum wallet transactions with advanced gas management and retry mechanisms.

## Features

- **Transaction Builder Pattern**: Fluent API for building and executing transactions
- **Gas Buffer Management**: Percentage-based gas buffer for safer transaction execution
- **Automatic Gas Estimation**: Auto-estimate gas with configurable safety buffers
- **Retry Mechanism**: Automatic retry with gas price adjustment for failed transactions
- **Transaction Monitoring**: Monitor transaction status and handle edge cases
- **Hook System**: Custom hooks for transaction lifecycle events

## Installation

```bash
go get github.com/tranvictor/walletarmy
```

## Quick Start

### Basic Transaction

```go
import (
    "github.com/tranvictor/walletarmy"
    "github.com/ethereum/go-ethereum/common"
)

// Create wallet manager
wm := walletarmy.NewWalletManager(...)

// Execute a transaction
tx, receipt, err := wm.R().
    SetFrom(fromAddress).
    SetTo(toAddress).
    SetValue(value).
    SetNetwork(network).
    Execute()
```

### Transaction with Gas Buffer

The gas buffer feature allows you to add a percentage-based safety margin to your gas limit:

```go
tx, receipt, err := wm.R().
    SetFrom(fromAddress).
    SetTo(contractAddress).
    SetGasLimit(0).              // 0 = Auto-estimate (e.g., 50,000)
    SetGasBufferPercent(0.40).   // Add 40% buffer (20,000)
    SetData(callData).
    SetNetwork(network).
    Execute()
// Result: Gas limit = 70,000 (50,000 + 40% buffer)
```

### Combining Gas Buffer with Extra Gas

You can combine percentage-based buffer with fixed extra gas:

```go
tx, receipt, err := wm.R().
    SetFrom(fromAddress).
    SetTo(contractAddress).
    SetGasLimit(0).              // Auto-estimate: 50,000
    SetGasBufferPercent(0.40).   // 40% buffer: +20,000 = 70,000
    SetExtraGasLimit(5000).      // Fixed extra: +5,000 = 75,000
    SetNetwork(network).
    Execute()
// Final gas limit = 75,000
```

## API Reference

### TxRequest Builder Methods

#### Gas Management

- **`SetGasLimit(gasLimit uint64)`** - Set fixed gas limit (0 = auto-estimate)
- **`SetGasBufferPercent(percent float64)`** - Set percentage-based gas buffer
  - Applied as: `gasLimit + (gasLimit × gasBufferPercent) + extraGasLimit`
  - Only applied when `percent > 0`
  - Example: `0.40` = 40% buffer
- **`SetExtraGasLimit(extraGas uint64)`** - Add fixed amount to gas limit
- **`SetGasPrice(gasPrice float64)`** - Set gas price in Gwei
- **`SetExtraGasPrice(extraGasPrice float64)`** - Add extra to gas price

#### Transaction Parameters

- **`SetFrom(from common.Address)`** - Set sender address
- **`SetTo(to common.Address)`** - Set recipient address
- **`SetValue(value *big.Int)`** - Set transaction value in Wei
- **`SetData(data []byte)`** - Set transaction data/calldata
- **`SetNetwork(network networks.Network)`** - Set target network

#### EIP-1559 Support

- **`SetTxType(txType uint8)`** - Set transaction type (0=legacy, 2=EIP-1559)
- **`SetTipCapGwei(tipCap float64)`** - Set priority fee (tip) in Gwei
- **`SetExtraTipCapGwei(extra float64)`** - Add extra to tip cap
- **`SetMaxGasPrice(maxPrice float64)`** - Set maximum gas price limit
- **`SetMaxTipCap(maxTip float64)`** - Set maximum tip cap limit

#### Retry Configuration

- **`SetNumRetries(retries int)`** - Set number of retry attempts
- **`SetSleepDuration(duration time.Duration)`** - Set sleep between retries
- **`SetTxCheckInterval(interval time.Duration)`** - Set transaction check interval

#### Hooks

- **`SetBeforeSignAndBroadcastHook(hook Hook)`** - Hook before signing
- **`SetAfterSignAndBroadcastHook(hook Hook)`** - Hook after broadcasting
- **`SetGasEstimationFailedHook(hook GasEstimationFailedHook)`** - Hook for gas estimation failures

#### Execution

- **`Execute()`** - Execute the transaction and return tx, receipt, error

## Gas Buffer Calculation

The gas buffer is calculated and applied in the following order:

1. **Base Gas**: Either auto-estimated or provided via `SetGasLimit()`
2. **Apply Buffer**: `bufferedGas = baseGas + (baseGas × gasBufferPercent)`
3. **Apply Extra**: `finalGas = bufferedGas + extraGasLimit`

### Examples

#### Example 1: Auto-estimate with 40% Buffer
```go
SetGasLimit(0)              // Estimates: 50,000
SetGasBufferPercent(0.40)   // Buffer: 50,000 × 0.40 = 20,000
// Final: 50,000 + 20,000 = 70,000
```

#### Example 2: Fixed Gas with 20% Buffer
```go
SetGasLimit(100000)         // Fixed: 100,000
SetGasBufferPercent(0.20)   // Buffer: 100,000 × 0.20 = 20,000
// Final: 100,000 + 20,000 = 120,000
```

#### Example 3: Buffer + Extra Gas
```go
SetGasLimit(0)              // Estimates: 50,000
SetGasBufferPercent(0.40)   // Buffer: 20,000 → Total: 70,000
SetExtraGasLimit(5000)      // Extra: 5,000
// Final: 70,000 + 5,000 = 75,000
```

## Best Practices

### Gas Buffer Recommendations

- **Simple Transfers**: 10-20% buffer (0.10 - 0.20)
- **Contract Calls**: 20-40% buffer (0.20 - 0.40)
- **Complex DeFi**: 40-50% buffer (0.40 - 0.50)
- **Unpredictable Contracts**: 50%+ buffer (0.50+)

### Why Use Gas Buffer?

Gas estimation isn't always perfect and can underestimate in these scenarios:
- Complex smart contract interactions
- Transactions with dynamic state changes
- Edge cases in contract execution paths
- Network congestion scenarios

A gas buffer provides a safety margin while still being cost-effective (unused gas is refunded).

## Examples

### DeFi Swap Transaction

```go
// Swap tokens on Uniswap with safety buffer
tx, receipt, err := wm.R().
    SetFrom(myAddress).
    SetTo(uniswapRouterAddress).
    SetData(swapCallData).
    SetGasLimit(0).              // Auto-estimate
    SetGasBufferPercent(0.40).   // 40% safety buffer
    SetTxType(2).                // EIP-1559
    SetTipCapGwei(2.0).          // Priority fee
    SetNetwork(networks.EthereumMainnet).
    Execute()
```

### NFT Minting with Retry

```go
// Mint NFT with retry on failure
tx, receipt, err := wm.R().
    SetFrom(myAddress).
    SetTo(nftContractAddress).
    SetData(mintCallData).
    SetGasLimit(0).
    SetGasBufferPercent(0.30).   // 30% buffer
    SetExtraGasLimit(10000).     // Extra 10k gas
    SetNumRetries(3).            // Retry 3 times
    SetSleepDuration(5 * time.Second).
    SetNetwork(network).
    Execute()
```

## Error Handling

```go
tx, receipt, err := wm.R().
    SetFrom(fromAddress).
    SetTo(toAddress).
    SetGasBufferPercent(0.40).
    Execute()

if err != nil {
    if errors.Is(err, walletarmy.ErrEstimateGasFailed) {
        // Handle gas estimation failure
    } else if errors.Is(err, walletarmy.ErrTxFailed) {
        // Handle transaction failure
    }
    // Handle other errors
}
```

## Development

### Using the Makefile

This project includes a Makefile for common development tasks:

```bash
# View all available commands
make help

# Run tests
make test              # Run all tests
make test-short        # Run tests in short mode
make test-coverage     # Generate coverage report
make test-race         # Run tests with race detector
make gas-buffer-test   # Run only gas buffer tests

# Code quality
make fmt               # Format code
make fmt-check         # Check code formatting
make vet               # Run go vet
make lint              # Run linters (requires golangci-lint)

# Build and dependencies
make build             # Build the project
make deps              # Download dependencies
make tidy              # Tidy go modules
make verify            # Verify dependencies

# Pre-commit and CI
make pre-commit        # Run pre-commit checks (fmt, vet, test-short)
make check             # Run all checks (fmt-check, vet, test)
make ci                # Run full CI pipeline

# Utilities
make clean             # Clean build artifacts and test cache
make install-tools     # Install development tools
make benchmark         # Run benchmarks
```

### Quick Development Workflow

```bash
# Before committing
make pre-commit

# Generate coverage report
make test-coverage

# Run full CI pipeline locally
make ci
```

## Contributing

Contributions are welcome! Please ensure:
- All tests pass (`make test` or `go test ./...`)
- Code is formatted (`make fmt`)
- Code passes linting (`make vet`)
- New features include tests and documentation
- Run `make pre-commit` before committing

## License

[License information]