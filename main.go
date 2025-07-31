package main

import (
	"fmt"
	"log"
	"math/big"

	"github.com/stake-plus/npos-calculator/elections"
	"github.com/stake-plus/npos-calculator/rpc"
	"github.com/stake-plus/npos-calculator/utils"
)

func main() {
	// Create Polkadot API client
	api, err := rpc.NewPolkadotAPI("wss://polkadot.dotters.network")
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}
	defer api.Close()

	// Fetch chain data (validators and nominators)
	chainData, err := api.FetchChainData()
	if err != nil {
		log.Fatal("Failed to fetch chain data:", err)
	}

	fmt.Printf("Found %d validators and %d nominators\n",
		len(chainData.Validators), len(chainData.Nominators))
	fmt.Printf("Active set size: %d\n", chainData.ValidatorCount)

	// Run NPoS election algorithm
	fmt.Println("Running NPoS election algorithm...")
	election := elections.NewElection(chainData)
	assignments := election.Run()

	// Calculate likelihood scores
	fmt.Println("Calculating likelihood scores...")
	scores := elections.CalculateLikelihoodScores(assignments, chainData)

	// Display results
	fmt.Printf("\nTop 800 Validator Rankings:\n")
	fmt.Println("===========================")
	fmt.Printf("%-5s %-12s %-12s %-8s %-11s %-10s\n",
		"Rank", "Validator", "Total Stake", "Backing", "Likelihood", "Status")
	fmt.Printf("%-5s %-12s %-12s %-8s %-11s %-10s\n",
		"----", "---------", "-----------", "-------", "----------", "------")

	// Show top 800 validators
	maxDisplay := 800
	if len(scores) < maxDisplay {
		maxDisplay = len(scores)
	}

	for i := 0; i < maxDisplay; i++ {
		score := scores[i]
		status := "Waiting"
		if score.Likelihood == 1.0 {
			status = "Active"
		}

		// Get validator to check commission
		var commission uint32
		for _, v := range chainData.Validators {
			if v.AccountID == score.ValidatorID {
				commission = v.Commission
				break
			}
		}

		// Display commission as percentage
		commissionPercent := float64(commission) / 10000000.0

		fmt.Printf("%-5d %-12s %-12s %-8d %-11s %-10s (%.1f%% commission)\n",
			i+1,
			score.ValidatorID[:12]+"...",
			utils.FormatBalance(score.TotalStake),
			score.BackingCount,
			fmt.Sprintf("%.2f%%", score.Likelihood*100),
			status,
			commissionPercent,
		)
	}

	// Summary
	activeCount := 0
	waitingCount := 0
	for i := 0; i < maxDisplay && i < len(scores); i++ {
		if scores[i].Likelihood == 1.0 {
			activeCount++
		} else {
			waitingCount++
		}
	}

	// Find minimum stake for active set
	minStake := new(big.Int)
	for i, score := range scores {
		if i == int(chainData.ValidatorCount)-1 && score.Likelihood == 1.0 {
			minStake = score.BackedStake
			break
		}
	}

	fmt.Printf("\nSummary:\n")
	fmt.Printf("Total validators: %d\n", len(scores))
	fmt.Printf("Showing top: %d\n", maxDisplay)
	fmt.Printf("Active validators (in top %d): %d\n", maxDisplay, activeCount)
	fmt.Printf("Waiting validators (in top %d): %d\n", maxDisplay, waitingCount)
	fmt.Printf("Minimum stake for active set: %s\n", utils.FormatBalance(minStake))
}
