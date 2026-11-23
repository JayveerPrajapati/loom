package flow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

// NewID returns a time-sortable-ish random identifier. The timestamp prefix
// keeps CLI listings readable; randomness prevents collisions.
func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A predictable identifier is worse than a crash: ids must not collide
		// and never carry a known constant.
		panic("flow: crypto/rand failed: " + err.Error())
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

// NewToken returns a 128-bit resume token.
func NewToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// This token is an approval capability URL. Returning a known constant
		// on RNG failure would let anyone decide waiting runs.
		panic("flow: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func CloneItem(item Item) Item {
	if item == nil {
		return Item{}
	}
	data, err := json.Marshal(item)
	if err != nil {
		return item
	}
	var out Item
	if err := json.Unmarshal(data, &out); err != nil {
		return item
	}
	return out
}
