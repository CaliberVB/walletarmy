package walletarmy

import "fmt"

// Transaction execution errors
var (
	ErrEstimateGasFailed    = fmt.Errorf("estimate gas failed")
	ErrAcquireNonceFailed   = fmt.Errorf("acquire nonce failed")
	ErrGetGasSettingFailed  = fmt.Errorf("get gas setting failed")
	ErrEnsureTxOutOfRetries = fmt.Errorf("ensure tx out of retries")
	ErrGasPriceLimitReached = fmt.Errorf("gas price protection limit reached")
	ErrFromAddressZero      = fmt.Errorf("from address cannot be zero")
	ErrNetworkNil           = fmt.Errorf("network cannot be nil")
	ErrSimulatedTxReverted  = fmt.Errorf("tx will be reverted")
	ErrSimulatedTxFailed     = fmt.Errorf("couldn't simulate tx at pending state")
	ErrCircuitBreakerOpen    = fmt.Errorf("circuit breaker is open: network temporarily unavailable")
	ErrSyncBroadcastTimeout  = fmt.Errorf("sync broadcast timed out, falling back to async monitoring")
)
