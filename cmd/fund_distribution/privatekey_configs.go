package main

import (
	"encoding/json"
	"fmt"
	"os"
)

const WALLET_COUNT = 30

type PrivateKeyConfig struct {
	PrivateKey string `json:"private_key"`
	Address    string `json:"address"`
}

type PrivateKeyConfigs []PrivateKeyConfig

func LoadPrivateKeyConfigs(path string) (PrivateKeyConfigs, error) {
	// load the json file
	// unmarshal the file into a PrivateKeyConfigs
	// return the PrivateKeyConfigs

	var privateKeyConfigs PrivateKeyConfigs

	jsonFile, err := os.Open(path)
	if err == nil {
		// if the file exists, read json into privateKeyConfigs
		err = json.NewDecoder(jsonFile).Decode(&privateKeyConfigs)
		if err != nil {
			// if the file exists but is not valid json, ignore and go ahead with empty configs
			fmt.Printf("Error decoding private key configs: %s. Ignoring file. Continuing with empty configs.\n", err)
		}
	}

	var selectedPrivateKeyConfigs PrivateKeyConfigs

	// if the number of private key configs is < WALLET_COUNT, create new wallets
	if len(privateKeyConfigs) < WALLET_COUNT {
		// create new wallets
		for i := len(privateKeyConfigs); i < WALLET_COUNT; i++ {
			// using secure random hex generator to generate a private key
			privateKey, address, err := GenerateEthereumKeyPair()
			if err != nil {
				return nil, err
			}
			// add the new private key config to the list
			privateKeyConfigs = append(privateKeyConfigs, PrivateKeyConfig{PrivateKey: privateKey, Address: address})
		}

		// save the new private key configs to the file
		err = SavePrivateKeyConfigs(path, privateKeyConfigs)
		if err != nil {
			return nil, err
		}

		selectedPrivateKeyConfigs = privateKeyConfigs
	} else {
		// if the number of private key configs is >= WALLET_COUNT, use the first WALLET_COUNT wallets
		selectedPrivateKeyConfigs = privateKeyConfigs[:WALLET_COUNT]
	}

	return selectedPrivateKeyConfigs, nil
}

func SavePrivateKeyConfigs(path string, privateKeyConfigs PrivateKeyConfigs) error {
	// marshal the private key configs to json
	json, err := json.Marshal(privateKeyConfigs)
	if err != nil {
		return err
	}
	// write the json to the file
	return os.WriteFile(path, json, 0o644)
}
