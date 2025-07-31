package main

import (
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"

	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/vedhavyas/go-subkey"
)

type ValidatorWork struct {
	storageKey types.StorageKey
	accountID  []byte
}

type ValidatorResult struct {
	validator *Validator
	err       error
}

func (p *PolkadotAPI) fetchValidators(meta *types.Metadata) ([]Validator, error) {
	fmt.Println("Fetching validators...")

	// Create storage key prefix
	prefix := createStorageKeyPrefix("Staking", "Validators")

	// Query all validator keys
	keys, err := p.api.RPC.State.GetKeysLatest(prefix)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Found %d validator entries\n", len(keys))

	// Create work queue
	workQueue := make(chan ValidatorWork, 100)
	results := make(chan ValidatorResult, 100)

	// Stats
	var successCount, errorCount atomic.Int32

	// Start workers
	numWorkers := 10
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			p.validatorWorker(workerID, meta, workQueue, results, &successCount, &errorCount)
		}(i)
	}

	// Start result collector
	validators := make([]Validator, 0, len(keys))
	done := make(chan bool)
	debugCount := 0

	go func() {
		for result := range results {
			if result.err == nil && result.validator != nil {
				validators = append(validators, *result.validator)

				count := int(successCount.Load())
				if debugCount < 5 {
					// Display commission as percentage (perbill / 10,000,000)
					commissionPercent := float64(result.validator.Commission) / 10000000.0
					fmt.Printf("Validator %s: stake=%s, commission=%.1f%%\n",
						result.validator.AccountID[:8]+"...",
						formatBalance(result.validator.SelfStake),
						commissionPercent)
					debugCount++
				} else if count%100 == 0 {
					fmt.Printf("Processed %d validators...\n", count)
				}
			}
		}
		done <- true
	}()

	// Queue work
	for _, storageKey := range keys {
		accountID := extractAccountFromStorageKey(storageKey)
		if len(accountID) == 32 {
			workQueue <- ValidatorWork{
				storageKey: storageKey,
				accountID:  accountID,
			}
		} else {
			errorCount.Add(1)
		}
	}

	close(workQueue)
	wg.Wait()
	close(results)
	<-done

	fmt.Printf("Successfully loaded %d validators (errors: %d)\n", len(validators), errorCount.Load())
	return validators, nil
}

func (p *PolkadotAPI) validatorWorker(
	workerID int,
	meta *types.Metadata,
	workQueue <-chan ValidatorWork,
	results chan<- ValidatorResult,
	successCount, errorCount *atomic.Int32,
) {
	for work := range workQueue {
		// Convert to SS58 address using the chain's prefix
		address := subkey.SS58Encode(work.accountID, p.ss58Prefix)

		// Get validator preferences
		var prefs ValidatorPrefs
		ok, err := p.api.RPC.State.GetStorageLatest(work.storageKey, &prefs)
		if err != nil || !ok {
			errorCount.Add(1)
			continue
		}

		// Convert UCompact to regular U32
		commission := uint32(prefs.Commission.Int64())

		// Sanity check commission - Perbill max is 1,000,000,000 (100%)
		if commission > 1000000000 {
			fmt.Printf("WARNING: Invalid commission %d for validator %s\n", commission, address[:8])
			// Cap at 100%
			commission = 1000000000
		}

		// Get real staking info
		stake := big.NewInt(0)
		stakeAmount, err := p.getStakingInfoSafe(meta, work.accountID)
		if err == nil && stakeAmount != nil {
			stake = stakeAmount
		}

		results <- ValidatorResult{
			validator: &Validator{
				AccountID:  address,
				SelfStake:  stake,
				TotalStake: stake,
				Commission: commission,
				Blocked:    bool(prefs.Blocked),
			},
			err: nil,
		}

		successCount.Add(1)
	}
}
