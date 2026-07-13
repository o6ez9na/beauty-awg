package awg

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// ObfuscationParams are the AmneziaWG anti-DPI knobs that live in [Interface].
// CRITICAL: hub, every node and every client MUST share identical values or the
// handshake fails. Generate once per deployment, store on the Hub row, reuse.
//
//	Jc         junk packet count (1..128 typical 3..10)
//	Jmin/Jmax  junk packet size bounds (Jmin < Jmax, <= 1280)
//	S1/S2      init/response packet junk header size
//	H1..H4     magic header values, must be distinct
type ObfuscationParams struct {
	Jc   int
	Jmin int
	Jmax int
	S1   int
	S2   int
	H1   uint32
	H2   uint32
	H3   uint32
	H4   uint32
}

// NewRandomParams produces a valid, self-consistent obfuscation profile.
func NewRandomParams() (ObfuscationParams, error) {
	h := make([]uint32, 4)
	seen := map[uint32]bool{}
	for i := range h {
		for {
			v, err := randUint32(0x10000000, 0x7FFFFFFF)
			if err != nil {
				return ObfuscationParams{}, err
			}
			if !seen[v] {
				seen[v] = true
				h[i] = v
				break
			}
		}
	}
	return ObfuscationParams{
		Jc:   randIntMust(3, 10),
		Jmin: 50,
		Jmax: 1000,
		S1:   randIntMust(15, 150),
		S2:   randIntMust(15, 150),
		H1:   h[0], H2: h[1], H3: h[2], H4: h[3],
	}, nil
}

// Validate checks the interdependencies AmneziaWG enforces.
func (p ObfuscationParams) Validate() error {
	if p.Jmin >= p.Jmax {
		return fmt.Errorf("Jmin(%d) must be < Jmax(%d)", p.Jmin, p.Jmax)
	}
	if p.Jc < 1 || p.Jc > 128 {
		return fmt.Errorf("Jc(%d) out of range 1..128", p.Jc)
	}
	seen := map[uint32]bool{}
	for _, v := range []uint32{p.H1, p.H2, p.H3, p.H4} {
		if seen[v] {
			return fmt.Errorf("H1..H4 must be distinct")
		}
		seen[v] = true
	}
	return nil
}

func randUint32(min, max uint32) (uint32, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		return 0, err
	}
	return min + uint32(n.Int64()), nil
}

func randIntMust(min, max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		panic(err)
	}
	return min + int(n.Int64())
}
