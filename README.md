# WalletArmy

A robust, production-ready Go library for managing Ethereum wallets and executing transactions with automatic retry logic, gas management, and resilience features.

## Features

- **Multi-wallet Management**: Manage multiple wallets across multiple networks simultaneously
- **Automatic Nonce Management**: Race-safe nonce acquisition with automatic release on failure
- **Smart Gas Handling**: Automatic gas estimation, price suggestions, and dynamic gas bumping for slow transactions
- **Retry Logic**: Configurable retry mechanism with exponential backoff for failed transactions
- **Circuit Breaker**: Protects against cascading failures from unreliable RPC nodes
- **Idempotency**: Prevents duplicate transaction submissions with idempotency keys
- **EIP-1559 Support**: Full support for dynamic fee transactions
- **Hook System**: Extensible hooks for custom logic at various transaction lifecycle stages
- **Context Support**: Full context.Context integration for cancellation and timeouts
- **Builder Pattern API**: Fluent, easy-to-use API similar to go-resty

## Installation

```bash
go get github.com/tranvictor/walletarmy
```

## Quick Start

### Basic Usage

```go
package main

import (
    "context"
    "math/big"
    "time"

    "github.com/ethereum/go-ethereum/common"
    "github.com/tranvictor/jarvis/networks"
    "github.com/tranvictor/walletarmy"
)

func main() {
    // Create a new wallet manager with default configuration
    wm := walletarmy.NewWalletManager(
        walletarmy.WithDefaultNumRetries(5),
        walletarmy.WithDefaultNetwork(networks.EthereumMainnet),
    )

    // Unlock a wallet (assumes wallet is configured in jarvis)
    wallet := common.HexToAddress("0xYourWalletAddress")
    _, err := wm.UnlockAccount(wallet)
    if err != nil {
        panic(err)
    }

    // Execute a transaction using the builder pattern
    tx, receipt, err := wm.R().
        SetFrom(wallet).
        SetTo(common.HexToAddress("0xRecipientAddress")).
        SetValue(big.NewInt(1e18)). // 1 ETH
        SetNetwork(networks.EthereumMainnet).
        Execute()

    if err != nil {
        panic(err)
    }

    fmt.Printf("Transaction mined: %s\n", tx.Hash().Hex())
    fmt.Printf("Gas used: %d\n", receipt.GasUsed)
}
```

### Using Context for Cancellation

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

tx, receipt, err := wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetNetwork(networks.EthereumMainnet).
    ExecuteContext(ctx)
```

### Contract Interaction

```go
// Prepare contract call data
data := contractABI.Pack("transfer", recipient, amount)

tx, receipt, err := wm.R().
    SetFrom(wallet).
    SetTo(contractAddress).
    SetData(data).
    SetNetwork(networks.EthereumMainnet).
    SetAbis(contractABI). // For better error decoding
    Execute()
```

## Configuration

### WalletManager Options

```go
wm := walletarmy.NewWalletManager(
    // Retry configuration
    walletarmy.WithDefaultNumRetries(9),
    walletarmy.WithDefaultSleepDuration(5*time.Second),
    walletarmy.WithDefaultTxCheckInterval(5*time.Second),

    // Gas configuration
    walletarmy.WithDefaultExtraGasLimit(10000),
    walletarmy.WithDefaultExtraGasPrice(1.0),    // Extra gwei
    walletarmy.WithDefaultExtraTipCap(0.5),       // Extra gwei
    walletarmy.WithDefaultMaxGasPrice(100.0),     // Max gwei
    walletarmy.WithDefaultMaxTipCap(10.0),        // Max gwei

    // Network
    walletarmy.WithDefaultNetwork(networks.EthereumMainnet),

    // Transaction type (0 = legacy, 2 = EIP-1559)
    walletarmy.WithDefaultTxType(2),

    // Idempotency store (optional)
    walletarmy.WithDefaultIdempotencyStore(24*time.Hour),
)
```

### Per-Request Configuration

The builder pattern allows overriding defaults per request:

```go
tx, receipt, err := wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetNumRetries(3).                    // Override default
    SetMaxGasPrice(50.0).                // Gas price protection
    SetMaxTipCap(5.0).                   // Tip cap protection
    SetGasLimit(21000).                  // Fixed gas limit
    SetIdempotencyKey("unique-tx-id").   // Prevent duplicates
    Execute()
```

## Hooks

Hooks allow you to inject custom logic at various stages of transaction execution:

### BeforeSignAndBroadcast Hook

Called after the transaction is built but before signing:

```go
wm.R().
    SetBeforeSignAndBroadcastHook(func(tx *types.Transaction, err error) error {
        log.Printf("About to sign tx with nonce: %d", tx.Nonce())
        // Return an error to abort the transaction
        return nil
    }).
    // ... other settings ...
    Execute()
```

### AfterSignAndBroadcast Hook

Called after successful broadcast:

```go
wm.R().
    SetAfterSignAndBroadcastHook(func(tx *types.Transaction, err error) error {
        log.Printf("Broadcasted tx: %s", tx.Hash().Hex())
        return nil
    }).
    // ... other settings ...
    Execute()
```

### GasEstimationFailed Hook

Called when gas estimation fails (usually means the tx would revert):

```go
wm.R().
    SetGasEstimationFailedHook(func(tx *types.Transaction, abiError *abi.Error, revertParams any, revertMsgError, gasEstimationError error) (*big.Int, error) {
        log.Printf("Gas estimation failed: %v", gasEstimationError)
        // Return a gas limit to override and continue, or an error to stop
        return nil, nil // Continue with default behavior
    }).
    // ... other settings ...
    Execute()
```

### SimulationFailed Hook

Called when eth_call simulation shows the tx would revert:

```go
wm.R().
    SetSimulationFailedHook(func(tx *types.Transaction, revertData []byte, abiError *abi.Error, revertParams any, err error) (shouldRetry bool, retErr error) {
        log.Printf("Simulation failed: %v", err)
        // Return shouldRetry=true to retry, or an error to stop
        return false, err // Stop and return the error
    }).
    // ... other settings ...
    Execute()
```

### TxMined Hook

Called when a transaction is mined (success or revert):

```go
wm.R().
    SetTxMinedHook(func(tx *types.Transaction, receipt *types.Receipt) error {
        if receipt.Status == 0 {
            log.Printf("Transaction reverted: %s", tx.Hash().Hex())
        } else {
            log.Printf("Transaction succeeded: %s", tx.Hash().Hex())
        }
        return nil
    }).
    // ... other settings ...
    Execute()
```

## Idempotency

Prevent duplicate transactions with idempotency keys:

```go
// Configure idempotency store
wm := walletarmy.NewWalletManager(
    walletarmy.WithDefaultIdempotencyStore(24*time.Hour),
)

// Use the same key for retried requests
tx, receipt, err := wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetIdempotencyKey("payment-order-12345").
    Execute()

// Calling again with the same key returns the cached result
tx2, receipt2, err2 := wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetIdempotencyKey("payment-order-12345"). // Same key
    Execute()
// tx2 and receipt2 will be the same as tx and receipt
```

## Circuit Breaker

The circuit breaker protects against cascading failures from unreliable RPC nodes:

```go
// Check circuit breaker status
stats := wm.GetCircuitBreakerStats(networks.EthereumMainnet)
fmt.Printf("State: %s, Failures: %d\n", stats.State, stats.ConsecutiveFailures)

// Manually reset if needed
wm.ResetCircuitBreaker(networks.EthereumMainnet)

// Record success/failure manually (usually automatic)
wm.RecordNetworkSuccess(networks.EthereumMainnet)
wm.RecordNetworkFailure(networks.EthereumMainnet)
```

## Enabling Debug Logs

WalletArmy uses the `github.com/KyberNetwork/logger` package. Enable debug logging to troubleshoot nonce issues and transaction flow:

```go
import "github.com/KyberNetwork/logger"

func init() {
    // Set log level to debug
    logger.SetLevel(logger.DebugLevel)
}
```

Debug logs include:
- **Nonce acquisition**: What nonce was chosen and why
- **Nonce release**: When unused nonces are released
- **Transaction lifecycle**: Build, sign, broadcast, and mining status
- **Retry decisions**: Why retries are happening

Example debug output:
```
DEBUG acquireNonce: nonce acquired and reserved wallet=0x123... network=mainnet chain_id=1 acquired_nonce=42 mined_nonce=40 remote_pending=41 local_pending=41 decision="pending on nodes, using remote (>= local)"
DEBUG ReleaseNonce: nonce released successfully wallet=0x123... network=mainnet chain_id=1 released_nonce=42 new_stored=41
```

## Error Handling

WalletArmy provides specific error types for different failure scenarios:

```go
import "errors"

tx, receipt, err := wm.R()./* ... */.Execute()

if err != nil {
    switch {
    case errors.Is(err, walletarmy.ErrEstimateGasFailed):
        // Gas estimation failed - tx would likely revert
    case errors.Is(err, walletarmy.ErrAcquireNonceFailed):
        // Couldn't get nonce from network
    case errors.Is(err, walletarmy.ErrGetGasSettingFailed):
        // Couldn't get gas price from network
    case errors.Is(err, walletarmy.ErrEnsureTxOutOfRetries):
        // Exhausted all retries
    case errors.Is(err, walletarmy.ErrGasPriceLimitReached):
        // Gas price protection limit hit
    case errors.Is(err, walletarmy.ErrSimulatedTxReverted):
        // Simulation showed tx would revert
    case errors.Is(err, walletarmy.ErrCircuitBreakerOpen):
        // Network circuit breaker is open
    case errors.Is(err, context.Canceled):
        // Context was cancelled
    case errors.Is(err, context.DeadlineExceeded):
        // Context deadline exceeded
    }
}
```

## Architecture

### Key Components

- **WalletManager**: Central component managing wallets, networks, and transaction execution
- **TxRequest**: Builder pattern for constructing transaction requests
- **TxExecutionContext**: Holds mutable state during transaction execution
- **CircuitBreaker**: Protects against cascading RPC failures
- **IdempotencyStore**: Prevents duplicate transaction submissions

### Concurrency Safety

WalletArmy is designed for concurrent use:

- **Per-wallet locks**: Nonce operations are locked per wallet, not globally
- **Per-network locks**: Network infrastructure is locked per network
- **Atomic nonce acquisition**: Nonces are reserved atomically to prevent races
- **Thread-safe defaults**: Configuration access is protected by RWMutex

## Contributing

Contributions are welcome! Please follow these guidelines:

### Development Setup

```bash
# Clone the repository
git clone https://github.com/tranvictor/walletarmy.git
cd walletarmy

# Install dependencies
go mod download

# Run tests
go test ./...

# Build
go build ./...
```

### Code Style

- Follow standard Go conventions
- Use `gofmt` for formatting
- Add comments for exported functions
- Include debug logging for critical operations

### Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Add/update tests as needed
5. Run `go test ./...` to ensure tests pass
6. Commit your changes (`git commit -m 'Add amazing feature'`)
7. Push to the branch (`git push origin feature/amazing-feature`)
8. Open a Pull Request

### Reporting Issues

When reporting issues, please include:
- Go version
- WalletArmy version
- Network (mainnet, testnet, etc.)
- Debug logs if available
- Minimal reproduction case

## License

This project is open source. See the LICENSE file for details.

## Acknowledgments

- Built on top of [jarvis](https://github.com/tranvictor/jarvis) for Ethereum interactions
- Inspired by [go-resty](https://github.com/go-resty/resty) for the builder pattern API
