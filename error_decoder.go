// Author: https://github.com/piavgh

package walletarmy

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/rpc"
)

type ErrorDecoder struct {
	errorBySelector map[string]abi.Error // map from error selector to ABI error
}

func NewErrorDecoder(abis ...abi.ABI) (*ErrorDecoder, error) {
	if len(abis) == 0 {
		return nil, fmt.Errorf("at least one ABI must be provided")
	}

	errorBySelector := make(map[string]abi.Error)

	for _, abi := range abis {
		for _, err := range abi.Errors {
			selector := hex.EncodeToString(err.ID[:4])
			errorBySelector[selector] = err
		}
	}

	return &ErrorDecoder{
		errorBySelector: errorBySelector,
	}, nil
}

// Decode decodes the error from a contract call.
// It should always wrap the original error (using %w).
// It can only decode Solidity custom errors https://soliditylang.org/blog/2021/04/21/custom-errors/
func (d *ErrorDecoder) Decode(err error) (abiError *abi.Error, errorParams any, resultErr error) {
	origErr := err
	var dataErr rpc.DataError
	if !errors.As(err, &dataErr) {
		return nil, nil, fmt.Errorf("not a Solidity custom error: %w", err)
	}

	errorData := dataErr.ErrorData()
	if errorData == nil {
		return nil, nil, fmt.Errorf("no error data, original error: %w", origErr)
	}

	hexStr, ok := errorData.(string)
	if !ok {
		return nil, nil, fmt.Errorf("error data is not string, original error: %w", origErr)
	}

	hexStr = strings.TrimPrefix(hexStr, "0x")
	errorBytes, decodeErr := hex.DecodeString(hexStr)
	if decodeErr != nil {
		return nil, nil, fmt.Errorf("failed to decode error data: %v, original error: %w", decodeErr, origErr)
	}

	if len(errorBytes) < 4 {
		return nil, nil, fmt.Errorf("invalid error data length, original error: %w", origErr)
	}

	errorSelector := hex.EncodeToString(errorBytes[:4])
	if abiError, exists := d.errorBySelector[errorSelector]; exists {
		errParams, unpackErr := abiError.Unpack(errorBytes)
		if unpackErr != nil {
			return &abiError, nil, fmt.Errorf(
				"failed to unpack error selector %s: %v, original error: %w",
				abiError.Name,
				unpackErr,
				origErr,
			)
		}

		return &abiError, errParams, fmt.Errorf(
			"contract error: %s with params: %v, original error: %w",
			abiError.Name,
			errParams,
			origErr,
		)
	}

	return nil, nil, fmt.Errorf("unknown error: 0x%s, original error: %w", errorSelector, origErr)
}
