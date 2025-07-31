package rpc

import (
	"fmt"

	gsrpc "github.com/centrifuge/go-substrate-rpc-client/v4"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/stake-plus/npos-calculator/nominators"
	stypes "github.com/stake-plus/npos-calculator/types"
	"github.com/stake-plus/npos-calculator/validators"
)

type PolkadotAPI struct {
	api        *gsrpc.SubstrateAPI
	metadata   *types.Metadata
	ss58Prefix uint16
}

func NewPolkadotAPI(endpoint string) (*PolkadotAPI, error) {
	fmt.Printf("Connecting to %s...\n", endpoint)

	api, err := gsrpc.NewSubstrateAPI(endpoint)
	if err != nil {
		return nil, err
	}

	return &PolkadotAPI{api: api}, nil
}

func (p *PolkadotAPI) Close() {
	if p.api != nil && p.api.Client != nil {
		p.api.Client.Close()
	}
}

func (p *PolkadotAPI) FetchChainData() (*stypes.ChainData, error) {
	fmt.Println("Fetching chain data...")

	// Get metadata
	meta, err := p.api.RPC.State.GetMetadataLatest()
	if err != nil {
		return nil, err
	}
	p.metadata = meta

	// Get chain info
	chain, err := p.api.RPC.System.Chain()
	if err != nil {
		return nil, err
	}

	// Get runtime version
	version, err := p.api.RPC.State.GetRuntimeVersionLatest()
	if err != nil {
		return nil, err
	}

	fmt.Printf("Connected to %s (spec v%d, metadata v%d)\n",
		chain, version.SpecVersion, meta.Version)

	// Get SS58 prefix from constants
	p.ss58Prefix, err = p.getSS58Prefix(meta)
	if err != nil {
		fmt.Printf("Warning: Could not get SS58 prefix, defaulting to 0: %v\n", err)
		p.ss58Prefix = 0
	} else {
		fmt.Printf("Using SS58 prefix: %d\n", p.ss58Prefix)
	}

	// Get current era
	var era types.U32
	key, err := types.CreateStorageKey(meta, "Staking", "CurrentEra")
	if err != nil {
		return nil, err
	}
	_, err = p.api.RPC.State.GetStorageLatest(key, &era)
	if err != nil {
		return nil, err
	}

	// Get validator count
	var validatorCount types.U32
	key, err = types.CreateStorageKey(meta, "Staking", "ValidatorCount")
	if err != nil {
		return nil, err
	}
	_, err = p.api.RPC.State.GetStorageLatest(key, &validatorCount)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Current era: %d, Validator count: %d\n", era, validatorCount)

	// Fetch validators with concurrency
	validatorFetcher := validators.NewFetcher(p.api, p.ss58Prefix)
	validatorList, err := validatorFetcher.FetchValidators(meta)
	if err != nil {
		return nil, err
	}

	// Fetch nominators with concurrency
	nominatorFetcher := nominators.NewFetcher(p.api, p.ss58Prefix)
	nominatorList, err := nominatorFetcher.FetchNominators(meta)
	if err != nil {
		return nil, err
	}

	return &stypes.ChainData{
		Validators:     validatorList,
		Nominators:     nominatorList,
		ValidatorCount: uint32(validatorCount),
		Era:            uint32(era),
	}, nil
}

func (p *PolkadotAPI) getSS58Prefix(meta *types.Metadata) (uint16, error) {
	// Get the SS58Prefix constant from System pallet
	for _, module := range meta.AsMetadataV14.Pallets {
		if string(module.Name) == "System" {
			for _, constant := range module.Constants {
				if string(constant.Name) == "SS58Prefix" {
					// The value should be a U16
					if len(constant.Value) >= 2 {
						// Little endian U16
						return uint16(constant.Value[0]) | (uint16(constant.Value[1]) << 8), nil
					}
				}
			}
		}
	}
	return 0, fmt.Errorf("SS58Prefix constant not found")
}
