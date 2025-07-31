package types

import (
	"math/big"

	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
)

type ChainData struct {
	Validators     []Validator
	Nominators     []Nominator
	ValidatorCount uint32
	Era            uint32
}

type Validator struct {
	AccountID  string
	SelfStake  *big.Int
	TotalStake *big.Int
	Commission uint32
	Blocked    bool
}

// ValidatorPrefs matches the Polkadot runtime structure
// Commission is stored as Compact<Perbill> in the runtime!
type ValidatorPrefs struct {
	Commission types.UCompact `scale:",compact"`
	Blocked    types.Bool
}

type Nominator struct {
	AccountID string
	Stake     *big.Int
	Targets   []string
}

// StakingLedger matches the Polkadot runtime structure
type StakingLedger struct {
	Stash          types.AccountID
	Total          types.U128
	Active         types.U128
	Unlocking      []UnlockChunk `scale:"max=32"`
	ClaimedRewards []types.U32   `scale:"max=512"`
}

type UnlockChunk struct {
	Value types.U128
	Era   types.U32
}

// Nominations matches the Polkadot runtime structure
type Nominations struct {
	Targets     []types.AccountID `scale:"max=16"`
	SubmittedIn types.U32
	Suppressed  types.Bool
}
