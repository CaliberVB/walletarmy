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
    "fmt"
    "math/big"

    "github.com/ethereum/go-ethereum/common"
    "github.com/tranvictor/jarvis/networks"
    "github.com/tranvictor/jarvis/util/account"
    "github.com/tranvictor/walletarmy"
)

func main() {
    // Create a new wallet manager with default configuration
    wm := walletarmy.NewWalletManager(
        walletarmy.WithDefaultNumRetries(5),
        walletarmy.WithDefaultNetwork(networks.EthereumMainnet),
    )

    // Create account from private key (without 0x prefix)
    privateKey := "your_private_key_hex_without_0x_prefix"
    acc, err := account.NewPrivateKeyAccount(privateKey)
    if err != nil {
        panic(err)
    }

    // Register the account with the wallet manager
    wm.SetAccount(acc)

    // Get the wallet address
    wallet := acc.Address()
    fmt.Printf("Wallet address: %s\n", wallet.Hex())

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

## Managing Wallets and Networks

### Adding Wallets at Runtime

WalletArmy supports adding wallets dynamically. You can add as many wallets as needed:

```go
wm := walletarmy.NewWalletManager()

// Method 1: From private key string
privateKey1 := "abc123..." // hex string without 0x prefix
acc1, err := account.NewPrivateKeyAccount(privateKey1)
if err != nil {
    panic(err)
}
wm.SetAccount(acc1)

// Method 2: Add multiple wallets
privateKeys := []string{
    "privatekey1...",
    "privatekey2...",
    "privatekey3...",
}

for _, pk := range privateKeys {
    acc, err := account.NewPrivateKeyAccount(pk)
    if err != nil {
        panic(err)
    }
    wm.SetAccount(acc)
}

// Method 3: Using jarvis wallet store (if configured)
walletAddress := common.HexToAddress("0x...")
acc, err := wm.UnlockAccount(walletAddress)
if err != nil {
    panic(err)
}
// Account is automatically registered

// Access a registered account later
acc = wm.Account(walletAddress)
if acc == nil {
    fmt.Println("Account not found")
}
```

### Working with Networks

WalletArmy uses the jarvis networks package which supports many EVM networks out of the box:

```go
import "github.com/tranvictor/jarvis/networks"

// Built-in networks
networks.EthereumMainnet
networks.Goerli
networks.Sepolia
networks.BSCMainnet
networks.Polygon
networks.Arbitrum
networks.Optimism
networks.Avalanche
networks.Fantom
// ... and many more

// Get network by name
network, err := networks.GetNetwork("mainnet")

// Get network by chain ID
network, err := networks.GetNetworkByID(1)

// Use in transactions
tx, receipt, err := wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetNetwork(networks.Polygon). // Use Polygon network
    Execute()
```

### Creating Custom Networks

You can create custom networks for any EVM-compatible chain. Here's an example for creating a custom Optimism L2 network:

```go
import (
    "github.com/ethereum/go-ethereum/common"
    "github.com/tranvictor/jarvis/networks"
)

// Create a custom Optimism L2 network
customNetwork := networks.NewGenericOptimismNetwork(networks.GenericOptimismNetworkConfig{
    // Name: Primary identifier for the network
    // Used in logs, UI, and as the main lookup key
    Name: "rise-testnet",

    // AlternativeNames: Other names that can be used to look up this network
    // Useful for aliases like "rise", "risetestnet", etc.
    AlternativeNames: []string{"rise", "riselabs-testnet"},

    // ChainID: The unique chain identifier (EIP-155)
    // Must match the chain ID returned by the RPC node
    ChainID: 11155931,

    // NativeTokenSymbol: Symbol of the native gas token
    // Usually "ETH" for Optimism L2s, but could be custom
    NativeTokenSymbol: "ETH",

    // NativeTokenDecimal: Decimal places for the native token
    // Almost always 18 for ETH-based chains
    NativeTokenDecimal: 18,

    // BlockTime: Average block time in seconds
    // Used for estimating confirmation times
    BlockTime: 1,

    // NodeVariableName: Environment variable name for custom RPC URL
    // If set, the system will check os.Getenv("RISE_TESTNET_NODE") for a custom node URL
    NodeVariableName: "RISE_TESTNET_NODE",

    // DefaultNodes: Map of node name -> RPC URL
    // These are the default RPC endpoints if no custom node is configured
    DefaultNodes: map[string]string{
        "rise-public": "https://testnet.riselabs.xyz",
        // You can add multiple nodes for redundancy:
        // "backup-node": "https://backup.riselabs.xyz",
    },

    // BlockExplorerAPIKeyVariableName: Environment variable for block explorer API key
    // Used for fetching ABIs and verifying contracts
    BlockExplorerAPIKeyVariableName: "RISE_TESTNET_SCAN_API_KEY",

    // BlockExplorerAPIURL: Base URL for the block explorer API (Etherscan-compatible)
    // Leave empty if no explorer API is available
    BlockExplorerAPIURL: "",

    // MultiCallContractAddress: Address of the Multicall3 contract
    // Used for batching multiple read calls into one RPC request
    // Multicall3 is deployed at the same address on most chains
    MultiCallContractAddress: common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11"),

    // SyncTxSupported: Whether the network supports eth_sendRawTransactionSync
    // This is an Optimism-specific RPC that returns the receipt immediately
    // Set to true for Optimism-based L2s that support this feature
    SyncTxSupported: true,
})

// Use the custom network in transactions
tx, receipt, err := wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetNetwork(customNetwork).
    Execute()
```

#### Network Types

The jarvis networks package supports different network types:

```go
// For standard EVM networks (Ethereum, BSC, Polygon, etc.)
networks.NewGenericNetwork(networks.GenericNetworkConfig{...})

// For Optimism-based L2s (Optimism, Base, custom OP Stack chains)
networks.NewGenericOptimismNetwork(networks.GenericOptimismNetworkConfig{...})

// For Arbitrum-based L2s
networks.NewGenericArbitrumNetwork(networks.GenericArbitrumNetworkConfig{...})
```

#### Using Environment Variables for RPC URLs

For production, it's recommended to use environment variables for RPC URLs:

```bash
# Set custom RPC URL
export RISE_TESTNET_NODE="https://your-private-rpc.com"

# Set block explorer API key (if available)
export RISE_TESTNET_SCAN_API_KEY="your-api-key"
```

The network will automatically use the environment variable if set, falling back to DefaultNodes otherwise.

### Synchronous Transaction Mining (eth_sendRawTransactionSync)

Some networks support `eth_sendRawTransactionSync`, a special RPC method that broadcasts a transaction and waits for it to be mined in a single call. This is particularly useful for L2 networks with fast block times.

#### How It Works

```
Standard Flow (eth_sendRawTransaction):
1. Build transaction
2. Sign transaction
3. Broadcast transaction → returns tx hash immediately
4. Poll for receipt (multiple RPC calls over time)
5. Transaction mined → return receipt

Sync Flow (eth_sendRawTransactionSync):
1. Build transaction
2. Sign transaction
3. Broadcast transaction → waits for mining → returns receipt immediately
   (Single RPC call that blocks until mined)
```

#### Benefits

- **Faster execution**: No polling loop required
- **Fewer RPC calls**: Single call instead of broadcast + multiple receipt checks
- **Simpler flow**: Immediate confirmation in the response
- **Ideal for L2s**: Fast block times (1-2 seconds) make sync calls practical

#### Supported Networks

Networks that support this feature include:
- **Optimism** and OP Stack chains (Base, Zora, Mode, Rise, etc.)
- **Arbitrum** chains
- Many custom L2 networks

#### Enabling Sync Transactions

When creating a custom network, set `SyncTxSupported: true`:

```go
customNetwork := networks.NewGenericOptimismNetwork(networks.GenericOptimismNetworkConfig{
    Name:    "my-l2-network",
    ChainID: 12345,
    // ... other config ...
    
    // Enable synchronous transaction support
    SyncTxSupported: true,
})
```

#### Automatic Detection

WalletArmy automatically uses sync transactions when available:

```go
// WalletArmy checks network.IsSyncTxSupported() internally
// If true, it uses BroadcastTxSync which returns the receipt immediately
// If false, it uses BroadcastTx and polls for the receipt

tx, receipt, err := wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetNetwork(optimismNetwork). // Supports sync tx
    Execute()

// receipt is available immediately after broadcast on sync-supported networks
// No additional polling was needed
```

#### Manual Sync Broadcast

You can also use sync broadcast directly:

```go
// Build and sign transaction
tx, err := wm.BuildTx(...)
_, signedTx, err := wm.SignTx(wallet, tx, network)

// Broadcast synchronously - blocks until mined
receipt, err := wm.BroadcastTxSync(signedTx)
if err != nil {
    // Handle error
}

// Receipt is immediately available
fmt.Printf("Mined in block: %d\n", receipt.BlockNumber)
fmt.Printf("Gas used: %d\n", receipt.GasUsed)
fmt.Printf("Status: %d\n", receipt.Status) // 1 = success, 0 = revert
```

#### Performance Comparison

| Network Type | Block Time | Sync Support | Typical Confirmation |
|-------------|-----------|--------------|---------------------|
| Ethereum Mainnet | ~12s | No | 12-24 seconds |
| Polygon | ~2s | No | 4-6 seconds |
| Optimism | ~2s | Yes | 2 seconds (single call) |
| Arbitrum | ~0.25s | Yes | <1 second (single call) |
| Base | ~2s | Yes | 2 seconds (single call) |

### Parallel Multi-Wallet Transactions

Execute transactions from multiple wallets concurrently:

```go
import "sync"

func executeParallelTransfers(wm *walletarmy.WalletManager, wallets []common.Address, recipient common.Address, network networks.Network) error {
    var wg sync.WaitGroup
    errChan := make(chan error, len(wallets))

    for _, wallet := range wallets {
        wg.Add(1)
        go func(from common.Address) {
            defer wg.Done()
            
            _, _, err := wm.R().
                SetFrom(from).
                SetTo(recipient).
                SetValue(big.NewInt(1e17)). // 0.1 ETH
                SetNetwork(network).
                Execute()
            
            if err != nil {
                errChan <- fmt.Errorf("transfer from %s failed: %w", from.Hex(), err)
            }
        }(wallet)
    }

    wg.Wait()
    close(errChan)

    // Collect errors
    var errors []error
    for err := range errChan {
        errors = append(errors, err)
    }

    if len(errors) > 0 {
        return fmt.Errorf("%d transfers failed", len(errors))
    }
    return nil
}
```

### Cross-Network Operations

Work with multiple networks simultaneously:

```go
wm := walletarmy.NewWalletManager()

// Register the same wallet for use on multiple networks
acc, _ := account.NewPrivateKeyAccount(privateKey)
wm.SetAccount(acc)
wallet := acc.Address()

// Send on Ethereum mainnet
wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetNetwork(networks.EthereumMainnet).
    Execute()

// Send on Polygon
wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetNetwork(networks.Polygon).
    Execute()

// Send on Arbitrum
wm.R().
    SetFrom(wallet).
    SetTo(recipient).
    SetValue(amount).
    SetNetwork(networks.Arbitrum).
    Execute()

// Each network maintains its own:
// - Nonce tracking
// - Gas price cache
// - RPC connections
// - Circuit breaker state
```

### Network Infrastructure Access

Access lower-level network components when needed:

```go
// Get the EthReader for a network (for read operations)
reader, err := wm.Reader(networks.EthereumMainnet)
if err != nil {
    panic(err)
}
balance, err := reader.GetBalance(wallet.Hex())

// Get the Broadcaster (for custom broadcast logic)
broadcaster, err := wm.Broadcaster(networks.EthereumMainnet)

// Get the TxAnalyzer (for transaction analysis)
analyzer, err := wm.Analyzer(networks.EthereumMainnet)

// Get current gas settings
gasInfo, err := wm.GasSetting(networks.EthereumMainnet)
fmt.Printf("Gas Price: %.2f gwei\n", gasInfo.GasPrice)
fmt.Printf("Tip Cap: %.2f gwei\n", gasInfo.MaxPriorityPrice)
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

### Project Structure

The codebase is organized for maintainability and reusability:

```
walletarmy/
├── manager.go              # Core WalletManager struct, constructors, account management
├── manager_nonce.go        # Nonce acquisition/release (delegates to internal/nonce)
├── manager_network.go      # Network infrastructure (reader, broadcaster, analyzer)
├── manager_tx.go           # Transaction building, signing, broadcasting, EnsureTx
├── request.go              # TxRequest builder pattern
├── execution_context.go    # TxExecutionContext for transaction state
├── interface.go            # Manager interface for mockability
├── hook.go                 # Hook interfaces (Hook, TxMinedHook, SimulationFailedHook)
├── errors.go               # Error definitions
├── types.go                # Core types (TxStatus, TxExecutionResult, ManagerDefaults)
├── options.go              # WalletManagerOption functional options
├── broadcast_error.go      # Broadcast error handling and detection
├── error_decoder.go        # ABI error decoding for contract reverts
├── gas_info.go             # Gas information types
├── idempotency/            # Idempotency store subpackage (public)
│   └── idempotency.go      # Store interface and InMemoryStore implementation
├── internal/
│   ├── circuitbreaker/
│   │   └── circuitbreaker.go   # Circuit breaker implementation
│   └── nonce/
│       └── tracker.go          # Thread-safe nonce tracking
├── examples/
│   └── fund_distribution/      # Example application
└── README.md
```

### File Responsibilities

| File | Responsibility |
|------|----------------|
| `manager.go` | WalletManager struct definition, NewWalletManager, account management, defaults access |
| `manager_nonce.go` | Public nonce API, delegates to internal/nonce tracker |
| `manager_network.go` | Reader, Broadcaster, Analyzer, initNetwork, GasSetting, circuit breaker access |
| `manager_tx.go` | BuildTx, SignTx, BroadcastTx, EnsureTx*, MonitorTx, transaction handlers |
| `request.go` | TxRequest builder with fluent API, Execute/ExecuteContext |
| `execution_context.go` | TxExecutionContext for managing retry state and gas adjustments |
| `interface.go` | Manager interface for dependency injection and mocking |
| `options.go` | Functional options for WalletManager configuration |
| `errors.go` | All error sentinel values (ErrEstimateGasFailed, etc.) |
| `types.go` | Core types and constants (TxStatus, ManagerDefaults, etc.) |
| `idempotency/` | Public subpackage: Store interface and InMemoryStore |
| `internal/nonce/` | Thread-safe nonce tracking with Tracker struct |
| `internal/circuitbreaker/` | Circuit breaker pattern for RPC resilience |

### Concurrency Safety

WalletArmy is designed for concurrent use:

- **Per-wallet locks**: Nonce operations are locked per wallet, not globally
- **Per-network locks**: Network infrastructure is locked per network
- **Atomic nonce acquisition**: Nonces are reserved atomically to prevent races
- **Thread-safe defaults**: Configuration access is protected by RWMutex
- **sync.Map usage**: Internal maps use sync.Map for lock-free concurrent access

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

### Testing

```bash
# Run all tests
go test ./...

# Run tests with race detection
go test -race ./...

# Run tests with coverage
go test -cover ./...

# Run specific package tests
go test ./internal/circuitbreaker/...
```

### Internal Packages

The `internal/` directory contains packages that are not part of the public API:

- **circuitbreaker**: A thread-safe circuit breaker implementation for RPC failover protection. Not exported to allow internal refactoring without breaking changes.
- **nonce**: Thread-safe nonce tracking for multiple wallets across multiple networks. Encapsulates all nonce acquisition, release, and tracking logic.

### Public Subpackages

- **idempotency**: Provides `Store` interface and `InMemoryStore` for preventing duplicate transactions. Import as `github.com/tranvictor/walletarmy/idempotency`.

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
