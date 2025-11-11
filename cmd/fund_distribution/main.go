package main

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/tranvictor/jarvis/common"
	"github.com/tranvictor/jarvis/networks"
	"github.com/tranvictor/jarvis/util"
	"github.com/tranvictor/jarvis/util/account"

	"github.com/tranvictor/walletarmy"
)

var L2_GAS_OVERHEAD = big.NewInt(2432225336832 * 10)

type TransferStrategyFunc func(privateKeyConfigs PrivateKeyConfigs, balances map[string]*big.Int) (initialBalances, targetBalances []*big.Int)

func ExecuteStrategy(
	network networks.Network,
	strategy TransferStrategyFunc,
	privateKeyConfigs PrivateKeyConfigs,
	balances map[string]*big.Int,
) (err error) {
	initialBalances, targetBalances := strategy(privateKeyConfigs, balances)

	transfers := findTransfers(initialBalances, targetBalances)

	fmt.Println("Transfers:")
	for _, transfer := range transfers {
		fmt.Printf(
			"Transfer from wallet %s to wallet %s: %s wei\n",
			privateKeyConfigs[transfer.From].Address,
			privateKeyConfigs[transfer.To].Address,
			transfer.Amount.String(),
		)
	}

	// command the army to transfer the funds
	fmt.Println("Commanding the army to transfer the funds...")

	// make the army context and load all of the wallets
	cm := walletarmy.NewWalletManager()
	for _, privateKeyConfig := range privateKeyConfigs {
		acc, err := account.NewPrivateKeyAccount(privateKeyConfig.PrivateKey)
		if err != nil {
			fmt.Printf("Error creating account from private key: %s\n", err)
			return err
		}
		cm.SetAccount(acc)
	}

	// loop through the transfers and execute them as fast as possible using the army context in parallel
	// this ensures all of the transfers are executed (transactions are mined and no transaction is dropped)

	startTime := time.Now()

	err = ParallelExecuteTransfers(cm, initialBalances, transfers, privateKeyConfigs, network)
	fmt.Println("Transfers executed successfully")

	if err != nil {
		fmt.Printf("However %s\n", err)
	}

	elapsedTime := time.Since(startTime)
	fmt.Printf("================ Time taken in seconds: %f\n", elapsedTime.Seconds())

	// Verify the balances
	balances, err = GetBalances(network, privateKeyConfigs)
	if err != nil {
		fmt.Printf("Error getting balances: %s\n", err)
		return err
	}

	fmt.Println("Balances:")
	for _, privateKeyConfig := range privateKeyConfigs {
		fmt.Printf("Address %s: %s wei\n", privateKeyConfig.Address, balances[privateKeyConfig.Address].String())
	}

	return nil
}

func main() {
	// load all private keys from a config file
	privateKeyConfigs, err := LoadPrivateKeyConfigs("private_keys.json")
	if err != nil {
		fmt.Printf("Error loading private key configs: %s\n", err)
		return
	}

	bitfiL2 := networks.BitfiTestnet

	shouldConsolidate := false
	for {
		// get the balance of each wallet
		balances, err := GetBalances(bitfiL2, privateKeyConfigs)
		if err != nil {
			fmt.Printf("Error getting balances: %s\n", err)
			return
		}

		fmt.Println("Our walltet army contains the following wallets:")
		for i, privateKeyConfig := range privateKeyConfigs {
			fmt.Printf(
				"Address %d: %s. ETH Balance: %s wei\n",
				i+1,
				privateKeyConfig.Address,
				balances[privateKeyConfig.Address].String(),
			)
		}
		if shouldConsolidate {
			err = ExecuteStrategy(bitfiL2, CalculateTargetBalancesForConsolidation, privateKeyConfigs, balances)
		} else {
			err = ExecuteStrategy(bitfiL2, CalculateTargetBalancesForEvenDistribution, privateKeyConfigs, balances)
		}
		if err != nil {
			fmt.Printf("Error executing strategy: %s\n", err)
			return
		}
		shouldConsolidate = !shouldConsolidate
	}
}

func ParallelExecuteTransfers(
	cm *walletarmy.WalletManager,
	initialBalances []*big.Int,
	transfers []Transfer,
	privateKeyConfigs PrivateKeyConfigs,
	network networks.Network,
) error {
	wg := sync.WaitGroup{}

	errChan := make(chan error, len(transfers))

	for _, transfer := range transfers {
		wg.Add(1)
		go func(transfer Transfer) {
			defer wg.Done()
			err := EnsureTransfer(
				cm,
				privateKeyConfigs[transfer.From],
				privateKeyConfigs[transfer.To],
				initialBalances[transfer.From],
				transfer.Amount,
				network,
			)
			if err != nil {
				err = fmt.Errorf(
					"error executing transfer from %s to %s with amount %s: %s",
					privateKeyConfigs[transfer.From].Address,
					privateKeyConfigs[transfer.To].Address,
					transfer.Amount.String(),
					err,
				)
				errChan <- err
			}
		}(transfer)
	}

	wg.Wait()
	close(errChan)

	errors := []error{}
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		errMsg := ""
		for _, err := range errors {
			errMsg += fmt.Sprintf("%s\n", err)
		}
		return fmt.Errorf("%s", errMsg)
	}

	return nil
}

// EnsureTransfer ensures that the transfer is executed and the transaction is mined, it is supposed to retry until the transfer is successful
func EnsureTransfer(
	cm *walletarmy.WalletManager,
	fromPrivateKeyConfig PrivateKeyConfig,
	toPrivateKeyConfig PrivateKeyConfig,
	initialBalance *big.Int,
	amount *big.Int,
	network networks.Network,
) (err error) {
	var tx *types.Transaction

	fromAddress := common.HexToAddress(fromPrivateKeyConfig.Address)
	toAddress := common.HexToAddress(toPrivateKeyConfig.Address)

	if initialBalance.Cmp(amount) <= 0 {
		gasInfo, gasErr := cm.GasSetting(network)
		if gasErr != nil {
			return gasErr
		}
		gasPrice := gasInfo.GasPrice

		amountAfterGas := big.NewInt(0).Sub(
			initialBalance,
			big.NewInt(0).Mul(
				big.NewInt(21000),
				common.GweiToWei(gasPrice),
			),
		)

		amountAfterGas = big.NewInt(0).Sub(amountAfterGas, L2_GAS_OVERHEAD)

		// send all of the funds to the to address
		tx, _, err = cm.EnsureTx(
			types.LegacyTxType, // tx type
			fromAddress, toAddress,
			amountAfterGas,
			21000,
			0,
			gasPrice,
			0,
			0,
			0,
			nil,
			network,
		)
	} else {
		tx, _, err = cm.EnsureTx(
			types.DynamicFeeTxType, // tx type
			fromAddress, toAddress,
			amount,  // amount
			0,       // gas limit
			0,       // extra gas limit
			0,       // gas price
			0,       // extra gas price
			0,       // tip cap
			0,       // extra tip cap
			nil,     // data
			network, // network
		)
	}

	if err != nil {
		return err
	}

	fmt.Printf(
		"Transfer from %s to %s with amount %s executed successfully with tx %s\n",
		fromPrivateKeyConfig.Address,
		toPrivateKeyConfig.Address,
		amount.String(),
		tx.Hash().Hex(),
	)

	return nil
}

// CalculateTargetBalancesForConsolidation calculates the target balances to send all of the funds to the first wallet
func CalculateTargetBalancesForConsolidation(
	privateKeyConfigs PrivateKeyConfigs,
	balances map[string]*big.Int,
) ([]*big.Int, []*big.Int) {
	totalBalance := big.NewInt(0)
	for _, balance := range balances {
		totalBalance.Add(totalBalance, balance)
	}

	initialBalances := make([]*big.Int, len(privateKeyConfigs))
	targetBalances := make([]*big.Int, len(privateKeyConfigs))
	for i, privateKeyConfig := range privateKeyConfigs {
		initialBalances[i] = balances[privateKeyConfig.Address]
		targetBalances[i] = big.NewInt(0)
	}
	targetBalances[0] = totalBalance

	return initialBalances, targetBalances
}

func CalculateTargetBalancesForEvenDistribution(
	privateKeyConfigs PrivateKeyConfigs,
	balances map[string]*big.Int,
) ([]*big.Int, []*big.Int) {
	totalBalance := big.NewInt(0)
	for _, balance := range balances {
		totalBalance.Add(totalBalance, balance)
	}

	amountPerWallet := big.NewInt(0).Div(totalBalance, big.NewInt(int64(len(privateKeyConfigs))))

	initialBalances := make([]*big.Int, len(privateKeyConfigs))
	targetBalances := make([]*big.Int, len(privateKeyConfigs))
	for i, privateKeyConfig := range privateKeyConfigs {
		initialBalances[i] = balances[privateKeyConfig.Address]
		targetBalances[i] = big.NewInt(0).Set(amountPerWallet)
	}

	return initialBalances, targetBalances
}

func GetBalances(bitfiL2 networks.Network, privateKeyConfigs PrivateKeyConfigs) (map[string]*big.Int, error) {
	result := make(map[string]*big.Int)

	reader, err := util.EthReader(bitfiL2)
	if err != nil {
		return nil, err
	}

	for _, privateKeyConfig := range privateKeyConfigs {
		balance, err := reader.GetBalance(privateKeyConfig.Address)
		if err != nil {
			return nil, err
		}
		result[privateKeyConfig.Address] = balance
	}

	return result, nil
}
