package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
)

// GenerateEthereumKeyPair generates a private key and its associated address in hexadecimal format.
func GenerateEthereumKeyPair() (privateKeyHex, addressHex string, err error) {
	// Generate a new private key
	privateKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate private key: %w", err)
	}

	// Convert the private key to bytes and then to hex
	privateKeyBytes := crypto.FromECDSA(privateKey)
	privateKeyHex = hex.EncodeToString(privateKeyBytes)

	// Derive the public key from the private key
	publicKey := privateKey.PublicKey

	// Generate the address from the public key
	address := crypto.PubkeyToAddress(publicKey)
	addressHex = address.Hex()

	return privateKeyHex, addressHex, nil
}
