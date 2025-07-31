package nominators

import (
	"bytes"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"

	gsrpc "github.com/centrifuge/go-substrate-rpc-client/v4"
	"github.com/centrifuge/go-substrate-rpc-client/v4/scale"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	stypes "github.com/stake-plus/npos-calculator/types"
	"github.com/stake-plus/npos-calculator/utils"
	"github.com/vedhavyas/go-subkey"
)

type NominatorWork struct {
	storageKey types.StorageKey
	accountID  []byte
}

type NominatorResult struct {
	nominator *stypes.Nominator
	err       error
}

type Fetcher struct {
	api        *gsrpc.SubstrateAPI
	ss58Prefix uint16
}

func NewFetcher(api *gsrpc.SubstrateAPI, ss58Prefix uint16) *Fetcher {
	return &Fetcher{
		api:        api,
		ss58Prefix: ss58Prefix,
	}
}

func (f *Fetcher) FetchNominators(meta *types.Metadata) ([]stypes.Nominator, error) {
	fmt.Println("Fetching nominators...")

	// Create storage key prefix
	prefix := utils.CreateStorageKeyPrefix("Staking", "Nominators")

	// Query all nominator keys
	keys, err := f.api.RPC.State.GetKeysLatest(prefix)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Found %d nominator entries\n", len(keys))

	// Create work queue
	workQueue := make(chan NominatorWork, 1000)
	results := make(chan NominatorResult, 1000)

	// Stats
	var processed, errorCount, skipped atomic.Int32

	// Start workers
	numWorkers := 200
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.nominatorWorker(meta, workQueue, results, &processed, &errorCount, &skipped)
		}()
	}

	// Start result collector
	nominators := make([]stypes.Nominator, 0, len(keys))
	done := make(chan bool)
	go func() {
		for result := range results {
			if result.err == nil && result.nominator != nil {
				nominators = append(nominators, *result.nominator)
				count := int(processed.Load())
				if count <= 3 {
					fmt.Printf("Nominator %s: stake=%s, targets=%d\n",
						result.nominator.AccountID[:8]+"...",
						utils.FormatBalance(result.nominator.Stake),
						len(result.nominator.Targets))
				} else if count%1000 == 0 {
					fmt.Printf("Processed %d nominators...\n", count)
				}
			}
		}
		done <- true
	}()

	// Queue work
	for _, storageKey := range keys {
		accountID := utils.ExtractAccountFromStorageKey(storageKey)
		if len(accountID) == 32 {
			workQueue <- NominatorWork{
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

	fmt.Printf("Successfully loaded %d nominators (errors: %d, skipped: %d)\n",
		len(nominators), errorCount.Load(), skipped.Load())

	return nominators, nil
}

func (f *Fetcher) nominatorWorker(
	meta *types.Metadata,
	workQueue <-chan NominatorWork,
	results chan<- NominatorResult,
	processed, errorCount, skipped *atomic.Int32,
) {
	for work := range workQueue {
		// Convert to SS58 address using the chain's prefix
		address := subkey.SS58Encode(work.accountID, f.ss58Prefix)

		// Get nominations
		var nominations stypes.Nominations
		ok, err := f.api.RPC.State.GetStorageLatest(work.storageKey, &nominations)
		if err != nil || !ok {
			errorCount.Add(1)
			continue
		}

		// Skip if no targets or suppressed
		if len(nominations.Targets) == 0 || bool(nominations.Suppressed) {
			skipped.Add(1)
			continue
		}

		// Get real staking info
		stake, err := f.GetStakingInfoSafe(meta, work.accountID)
		if err != nil || stake == nil || stake.Cmp(big.NewInt(0)) == 0 {
			skipped.Add(1)
			continue
		}

		// Convert targets to addresses using the chain's prefix
		targets := make([]string, 0, len(nominations.Targets))
		for _, target := range nominations.Targets {
			targetAddress := subkey.SS58Encode(target[:], f.ss58Prefix)
			targets = append(targets, targetAddress)
		}

		results <- NominatorResult{
			nominator: &stypes.Nominator{
				AccountID: address,
				Stake:     stake,
				Targets:   targets,
			},
			err: nil,
		}
		processed.Add(1)
	}
}

// GetStakingInfoSafe handles decoding errors
func (f *Fetcher) GetStakingInfoSafe(meta *types.Metadata, accountID []byte) (*big.Int, error) {
	// Get bonded controller
	bondedKey, err := types.CreateStorageKey(meta, "Staking", "Bonded", accountID)
	if err != nil {
		return nil, err
	}

	var raw types.StorageDataRaw
	exists, err := f.api.RPC.State.GetStorageLatest(bondedKey, &raw)
	if err != nil || !exists || len(raw) == 0 {
		return nil, fmt.Errorf("no bonded controller")
	}

	// Controller is 32 bytes
	if len(raw) < 32 {
		return nil, fmt.Errorf("invalid controller data")
	}
	controller := raw[:32]

	// Get ledger
	ledgerKey, err := types.CreateStorageKey(meta, "Staking", "Ledger", controller)
	if err != nil {
		return nil, err
	}

	var ledgerRaw types.StorageDataRaw
	exists, err = f.api.RPC.State.GetStorageLatest(ledgerKey, &ledgerRaw)
	if err != nil || !exists || len(ledgerRaw) == 0 {
		return nil, fmt.Errorf("no ledger data")
	}

	// Parse ledger manually using scale decoder
	decoder := scale.NewDecoder(bytes.NewReader(ledgerRaw))

	// Skip stash (32 bytes AccountID)
	var stash types.AccountID
	err = decoder.Decode(&stash)
	if err != nil {
		return nil, fmt.Errorf("failed to decode stash: %w", err)
	}

	// Read total (compact U128)
	var total types.UCompact
	err = decoder.Decode(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to decode total: %w", err)
	}

	// Read active (compact U128)
	var active types.UCompact
	err = decoder.Decode(&active)
	if err != nil {
		return nil, fmt.Errorf("failed to decode active: %w", err)
	}

	// Convert UCompact to big.Int
	activeAmount := active.Int64()
	return big.NewInt(activeAmount), nil
}
