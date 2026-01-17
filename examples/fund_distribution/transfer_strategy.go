package main

import (
	"math/big"
)

// Transfer represents a single transfer from one wallet to another.
type Transfer struct {
	From   int      // index of the wallet to transfer from
	To     int      // index of the wallet to transfer to
	Amount *big.Int // amount to transfer
}

// findTransfers calculates the transfers needed so that
// each wallet's balance matches its target balance.
// Both initial and target should be slices of *big.Int.
func findTransfers(initial []*big.Int, target []*big.Int) []Transfer {
	n := len(initial)
	if len(target) != n {
		panic("initial and target must have the same length")
	}

	// Step 1: Calculate the difference = initial[i] - target[i]
	difference := make([]*big.Int, n)
	excess := []int{}  // indices of wallets with surplus
	deficit := []int{} // indices of wallets with deficit

	for i := 0; i < n; i++ {
		diff := new(big.Int).Sub(initial[i], target[i])
		difference[i] = diff

		switch diff.Sign() {
		case 1: // diff > 0
			excess = append(excess, i)
		case -1: // diff < 0
			deficit = append(deficit, i)
		}
	}

	// Step 2: Match surpluses and deficits
	transfers := []Transfer{}
	i, j := 0, 0

	for i < len(excess) && j < len(deficit) {
		e := excess[i]
		d := deficit[j]

		// difference[e] is surplus, difference[d] is negative
		// minAmount = min( difference[e], -difference[d] )
		surplus := difference[e]
		need := new(big.Int).Neg(difference[d]) // -difference[d]

		transferAmount := minBig(surplus, need)

		transfers = append(transfers, Transfer{
			From:   e,
			To:     d,
			Amount: new(big.Int).Set(transferAmount), // copy for safety
		})

		// Update difference[e] and difference[d]
		difference[e].Sub(difference[e], transferAmount)
		difference[d].Add(difference[d], transferAmount)

		// Move pointers if fully resolved
		if difference[e].Sign() == 0 {
			i++
		}
		if difference[d].Sign() == 0 {
			j++
		}
	}

	return transfers
}

// minBig returns the smaller of two *big.Int values.
func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) < 0 {
		return a
	}
	return b
}
