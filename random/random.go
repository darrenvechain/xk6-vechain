package random

import (
	crand "crypto/rand"
	"encoding/binary"
	mrand "math/rand"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// prng is a pseudo random number generator seeded by strong randomness.
// The randomness is printed on startup in order to make failures reproducible.
var (
	prng = initRand()
	mu   sync.Mutex // mutex to protect concurrent access to prng
)

func initRand() *mrand.Rand {
	var seed [8]byte
	crand.Read(seed[:])
	rnd := mrand.New(mrand.NewSource(int64(binary.LittleEndian.Uint64(seed[:]))))
	return rnd
}

// Bytes generates a random byte slice with specified length.
func Bytes(n int) []byte {
	mu.Lock()
	defer mu.Unlock()
	r := make([]byte, n)
	prng.Read(r)
	return r
}

// Hash generates a random hash.
func Hash() common.Hash {
	return common.BytesToHash(Bytes(common.HashLength))
}

// Address generates a random address.
func Address() common.Address {
	return common.BytesToAddress(Bytes(common.AddressLength))
}

// Uint8 generates a random uint8.
func Uint8() uint8 {
	mu.Lock()
	defer mu.Unlock()
	return uint8(prng.Intn(256))
}

// Element returns a random element from the slice.
func Element[T any](slice []T) T {
	mu.Lock()
	defer mu.Unlock()
	return slice[prng.Intn(len(slice))]
}

// Intn returns a random integer in range [0, n).
func Intn(n int) int {
	mu.Lock()
	defer mu.Unlock()
	return prng.Intn(n)
}
