// Package shuffle provides deterministic scenario permutation.
//
// A fixed seed always produces the same order; a different seed only changes
// the order, never the participating scenario set. The input slice is never
// mutated: Order returns a fresh permutation.
package shuffle

import (
	"math/rand/v2"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

// Order returns a seeded Fisher-Yates permutation of scenarios.
func Order(scenarios []*scenario.Scenario, seed uint64) []*scenario.Scenario {
	out := make([]*scenario.Scenario, len(scenarios))
	copy(out, scenarios)
	rng := rand.New(rand.NewChaCha8(seedToBytes(seed)))
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func seedToBytes(seed uint64) [32]byte {
	var b [32]byte
	for i := 0; i < 8; i++ {
		b[i] = byte(seed >> (8 * i))
	}
	// Repeat the seed across the full 256-bit key so arbitrary uint64 seeds
	// map to distinct, well-defined ChaCha8 states.
	for i := 8; i < 32; i++ {
		b[i] = b[i%8] ^ byte(i)
	}
	return b
}
