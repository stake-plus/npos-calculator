package utils

import (
	"fmt"
	"math/big"

	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/centrifuge/go-substrate-rpc-client/v4/xxhash"
)

func CreateStorageKeyPrefix(module, storage string) types.StorageKey {
	// Create the key prefix using xxhash
	moduleHash := xxhash.New128([]byte(module)).Sum(nil)
	storageHash := xxhash.New128([]byte(storage)).Sum(nil)

	// Combine the hashes
	prefix := make([]byte, 32)
	copy(prefix[0:16], moduleHash)
	copy(prefix[16:32], storageHash)

	return types.StorageKey(prefix)
}

func ExtractAccountFromStorageKey(key types.StorageKey) []byte {
	// The storage key format for maps:
	// [16 bytes module hash][16 bytes storage hash][hasher data][key data]
	// For Twox64Concat: [8 bytes hash][32 bytes account ID]
	keyBytes := []byte(key)

	// Check if we have enough bytes
	// 32 (prefix) + 8 (twox64 hash) + 32 (account ID) = 72 bytes
	if len(keyBytes) < 72 {
		return nil
	}

	// Skip prefix (32 bytes) and twox64 hash (8 bytes)
	accountBytes := keyBytes[40:72]

	return accountBytes
}

func FormatBalance(balance *big.Int) string {
	if balance == nil {
		return "0 DOT"
	}

	// Convert from Planck to DOT (10^10 Planck = 1 DOT)
	dotBalance := new(big.Float).SetInt(balance)
	dotBalance.Quo(dotBalance, big.NewFloat(1e10))

	f, _ := dotBalance.Float64()
	return fmt.Sprintf("%.2f DOT", f)
}
