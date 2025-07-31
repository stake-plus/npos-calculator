package main

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"sync"
)

type Election struct {
	chainData   *ChainData
	edges       []Edge
	assignments map[string]*Assignment
	mu          sync.RWMutex
}

type Edge struct {
	NominatorID string
	ValidatorID string
	Weight      *big.Int
	Load        float64
}

type Assignment struct {
	ValidatorID string
	BackedStake *big.Int
	Score       float64
	Elected     bool
	Nominators  []string
}

type ValidatorScore struct {
	ValidatorID  string
	TotalStake   *big.Int
	BackedStake  *big.Int
	Score        float64
	BackingCount int
	Likelihood   float64
}

func NewElection(chainData *ChainData) *Election {
	return &Election{
		chainData:   chainData,
		edges:       make([]Edge, 0),
		assignments: make(map[string]*Assignment),
	}
}

func (e *Election) Run() map[string]*Assignment {
	// Initialize edges and assignments
	e.initializeEdges()

	// Run Sequential Phragmén algorithm
	e.runSequentialPhragmen()

	// Post-process with load balancing
	e.balanceLoads()

	return e.assignments
}

func (e *Election) initializeEdges() {
	// Create validator assignments
	for _, validator := range e.chainData.Validators {
		e.assignments[validator.AccountID] = &Assignment{
			ValidatorID: validator.AccountID,
			BackedStake: big.NewInt(0).Set(validator.SelfStake),
			Score:       0,
			Elected:     false,
			Nominators:  make([]string, 0),
		}
	}

	// Create edges from nominators to validators using workers
	fmt.Printf("Creating edges for %d nominators...\n", len(e.chainData.Nominators))

	// Channel for collecting edges
	edgeChan := make(chan []Edge, 100)

	// Split nominators into chunks for parallel processing
	numWorkers := 200
	chunkSize := (len(e.chainData.Nominators) + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(e.chainData.Nominators) {
			end = len(e.chainData.Nominators)
		}

		wg.Add(1)
		go func(nominators []Nominator) {
			defer wg.Done()

			localEdges := make([]Edge, 0, len(nominators)*10)

			for _, nominator := range nominators {
				validTargets := 0
				for _, target := range nominator.Targets {
					if _, exists := e.assignments[target]; exists {
						validTargets++
					}
				}

				if validTargets == 0 {
					continue
				}

				// Initially distribute stake equally among targets
				stakePerTarget := new(big.Int).Div(nominator.Stake, big.NewInt(int64(validTargets)))

				for _, target := range nominator.Targets {
					if _, exists := e.assignments[target]; exists {
						localEdges = append(localEdges, Edge{
							NominatorID: nominator.AccountID,
							ValidatorID: target,
							Weight:      new(big.Int).Set(stakePerTarget),
							Load:        0,
						})
					}
				}
			}

			edgeChan <- localEdges
		}(e.chainData.Nominators[start:end])
	}

	// Collect results
	go func() {
		wg.Wait()
		close(edgeChan)
	}()

	edgeCount := 0
	for localEdges := range edgeChan {
		e.edges = append(e.edges, localEdges...)
		edgeCount += len(localEdges)
		if edgeCount%10000 == 0 {
			fmt.Printf("Created %d edges...\n", edgeCount)
		}
	}

	fmt.Printf("Created %d edges total\n", len(e.edges))
}

func (e *Election) runSequentialPhragmen() {
	numToElect := int(e.chainData.ValidatorCount)

	// Calculate approval stake for each validator
	approvalStake := make(map[string]*big.Int)
	for _, validator := range e.chainData.Validators {
		approvalStake[validator.AccountID] = new(big.Int).Set(validator.SelfStake)
	}

	for _, edge := range e.edges {
		if stake, exists := approvalStake[edge.ValidatorID]; exists {
			approvalStake[edge.ValidatorID] = new(big.Int).Add(stake, edge.Weight)
		}
	}

	// Track nominator loads
	nominatorLoad := make(map[string]float64)

	// Run election rounds
	fmt.Println("Running election rounds...")
	for round := 0; round < numToElect && round < len(e.chainData.Validators); round++ {
		if round%200 == 0 {
			fmt.Printf("Election round %d/%d\n", round, numToElect)
		}

		var bestCandidate string
		bestScore := math.Inf(1)

		// Calculate scores for unelected validators
		for validatorID, assignment := range e.assignments {
			if assignment.Elected {
				continue
			}

			stake := approvalStake[validatorID]
			if stake.Cmp(big.NewInt(0)) == 0 {
				continue
			}

			// Calculate score as 1/approval_stake
			score := 1.0 / float64(stake.Int64())

			// Add nominator load contributions
			for _, edge := range e.edges {
				if edge.ValidatorID == validatorID {
					load := nominatorLoad[edge.NominatorID]
					stakeFloat := float64(edge.Weight.Int64())
					approvalFloat := float64(stake.Int64())
					score += stakeFloat * load / approvalFloat
				}
			}

			if score < bestScore {
				bestScore = score
				bestCandidate = validatorID
			}
		}

		if bestCandidate == "" {
			break
		}

		// Elect the best candidate
		e.assignments[bestCandidate].Elected = true
		e.assignments[bestCandidate].Score = bestScore

		// Update nominator loads
		for i := range e.edges {
			if e.edges[i].ValidatorID == bestCandidate {
				nominatorLoad[e.edges[i].NominatorID] = bestScore
				e.edges[i].Load = bestScore
			}
		}
	}

	// Convert loads to weights
	e.loadsToWeights(nominatorLoad)
}

func (e *Election) loadsToWeights(nominatorLoad map[string]float64) {
	// Group edges by nominator
	edgesByNominator := make(map[string][]*Edge)
	for i := range e.edges {
		edge := &e.edges[i]
		edgesByNominator[edge.NominatorID] = append(edgesByNominator[edge.NominatorID], edge)
	}

	// Calculate weights based on loads
	for _, edges := range edgesByNominator {
		load := float64(0)
		var totalStake *big.Int

		// Find the nominator's total stake and max load
		for _, edge := range edges {
			if edge.Load > load {
				load = edge.Load
			}
		}

		if load == 0 {
			continue
		}

		// Find total stake for this nominator
		for _, nominator := range e.chainData.Nominators {
			if len(edges) > 0 && nominator.AccountID == edges[0].NominatorID {
				totalStake = nominator.Stake
				break
			}
		}

		if totalStake == nil {
			continue
		}

		// Calculate weight for each edge
		for _, edge := range edges {
			if e.assignments[edge.ValidatorID].Elected && edge.Load > 0 {
				weight := new(big.Float).SetInt(totalStake)
				weight.Mul(weight, big.NewFloat(edge.Load))
				weight.Quo(weight, big.NewFloat(load))

				weightInt, _ := weight.Int(nil)
				edge.Weight = weightInt

				// Update backed stake
				e.assignments[edge.ValidatorID].BackedStake = new(big.Int).Add(
					e.assignments[edge.ValidatorID].BackedStake,
					weightInt,
				)
				e.assignments[edge.ValidatorID].Nominators = append(
					e.assignments[edge.ValidatorID].Nominators,
					edge.NominatorID,
				)
			} else {
				edge.Weight = big.NewInt(0)
			}
		}
	}

	// Add self stake for elected validators
	for _, validator := range e.chainData.Validators {
		if assignment, exists := e.assignments[validator.AccountID]; exists && assignment.Elected {
			assignment.BackedStake = new(big.Int).Add(assignment.BackedStake, validator.SelfStake)
		}
	}
}

func (e *Election) balanceLoads() {
	// Implement load balancing to equalize backed stakes
	maxIterations := 100
	tolerance := 0.1

	fmt.Println("Balancing loads...")

	for iter := 0; iter < maxIterations; iter++ {
		improved := false

		// Group edges by nominator
		edgesByNominator := make(map[string][]*Edge)
		for i := range e.edges {
			edge := &e.edges[i]
			if e.assignments[edge.ValidatorID].Elected && edge.Weight.Cmp(big.NewInt(0)) > 0 {
				edgesByNominator[edge.NominatorID] = append(edgesByNominator[edge.NominatorID], edge)
			}
		}

		// Try to balance each nominator's stake
		for _, edges := range edgesByNominator {
			if len(edges) < 2 {
				continue
			}

			// Find min and max backed validators
			var minEdge, maxEdge *Edge
			minStake := new(big.Int).SetUint64(math.MaxUint64)
			maxStake := big.NewInt(0)

			for _, edge := range edges {
				backed := e.assignments[edge.ValidatorID].BackedStake
				if backed.Cmp(minStake) < 0 {
					minStake = backed
					minEdge = edge
				}
				if backed.Cmp(maxStake) > 0 {
					maxStake = backed
					maxEdge = edge
				}
			}

			if minEdge == nil || maxEdge == nil || minEdge.ValidatorID == maxEdge.ValidatorID {
				continue
			}

			// Calculate difference
			diff := new(big.Int).Sub(maxStake, minStake)
			diffFloat := new(big.Float).SetInt(diff)
			minFloat := new(big.Float).SetInt(minStake)

			ratio := new(big.Float).Quo(diffFloat, minFloat)
			ratioFloat, _ := ratio.Float64()

			if ratioFloat > tolerance {
				// Transfer some stake from max to min
				transferAmount := new(big.Int).Div(diff, big.NewInt(4))

				if transferAmount.Cmp(maxEdge.Weight) > 0 {
					transferAmount = new(big.Int).Div(maxEdge.Weight, big.NewInt(2))
				}

				if transferAmount.Cmp(big.NewInt(0)) > 0 {
					maxEdge.Weight = new(big.Int).Sub(maxEdge.Weight, transferAmount)
					minEdge.Weight = new(big.Int).Add(minEdge.Weight, transferAmount)

					e.assignments[maxEdge.ValidatorID].BackedStake = new(big.Int).Sub(
						e.assignments[maxEdge.ValidatorID].BackedStake,
						transferAmount,
					)
					e.assignments[minEdge.ValidatorID].BackedStake = new(big.Int).Add(
						e.assignments[minEdge.ValidatorID].BackedStake,
						transferAmount,
					)

					improved = true
				}
			}
		}

		if !improved {
			break
		}
	}
}

func CalculateLikelihoodScores(assignments map[string]*Assignment, chainData *ChainData) []ValidatorScore {
	scores := make([]ValidatorScore, 0, len(chainData.Validators))

	// First, calculate total potential stake for ALL validators
	// This includes self stake + all nominators who have nominated them
	validatorTotalStake := make(map[string]*big.Int)
	validatorNominatorCount := make(map[string]int)

	// Initialize with self stake
	for _, validator := range chainData.Validators {
		validatorTotalStake[validator.AccountID] = new(big.Int).Set(validator.SelfStake)
		validatorNominatorCount[validator.AccountID] = 0
	}

	// Add nominator stakes
	for _, nominator := range chainData.Nominators {
		for _, target := range nominator.Targets {
			if totalStake, exists := validatorTotalStake[target]; exists {
				validatorTotalStake[target] = new(big.Int).Add(totalStake, nominator.Stake)
				validatorNominatorCount[target]++
			}
		}
	}

	// Create validator scores
	for _, validator := range chainData.Validators {
		assignment, exists := assignments[validator.AccountID]

		score := ValidatorScore{
			ValidatorID:  validator.AccountID,
			TotalStake:   validatorTotalStake[validator.AccountID],
			BackedStake:  big.NewInt(0),
			Score:        0,
			BackingCount: validatorNominatorCount[validator.AccountID],
			Likelihood:   0,
		}

		if exists && assignment.Elected {
			score.BackedStake = assignment.BackedStake
			score.Score = assignment.Score
			score.Likelihood = 1.0 // Already elected
		} else {
			// For non-elected validators, use total stake for sorting
			score.BackedStake = big.NewInt(0)
			if exists {
				score.Score = assignment.Score
			}
		}

		scores = append(scores, score)
	}

	// Sort by backed stake for elected, total stake for non-elected
	sort.Slice(scores, func(i, j int) bool {
		// Both elected - sort by backed stake
		if scores[i].BackedStake.Cmp(big.NewInt(0)) > 0 && scores[j].BackedStake.Cmp(big.NewInt(0)) > 0 {
			return scores[i].BackedStake.Cmp(scores[j].BackedStake) > 0
		}

		// One elected, one not - elected comes first
		if scores[i].BackedStake.Cmp(big.NewInt(0)) > 0 {
			return true
		}
		if scores[j].BackedStake.Cmp(big.NewInt(0)) > 0 {
			return false
		}

		// Both non-elected - sort by total stake
		return scores[i].TotalStake.Cmp(scores[j].TotalStake) > 0
	})

	// Calculate likelihood for non-elected validators
	activeSetSize := int(chainData.ValidatorCount)
	for i := range scores {
		if scores[i].Likelihood == 0 {
			if i < activeSetSize {
				// Would be elected based on current ranking
				scores[i].Likelihood = 0.9 - (float64(i) / float64(activeSetSize) * 0.3)
			} else {
				// Outside active set
				position := i - activeSetSize + 1
				scores[i].Likelihood = math.Max(0.0, 0.5-float64(position)*0.01)
			}
		}
	}

	return scores
}
