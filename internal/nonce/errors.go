package nonce

import "fmt"

var (
	// ErrAbnormalNonceState is returned when mined nonce > pending nonce
	ErrAbnormalNonceState = fmt.Errorf("mined nonce is higher than pending nonce, this is abnormal data from nodes, retry again later")
)
