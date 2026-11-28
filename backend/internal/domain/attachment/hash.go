package attachment

import (
	"crypto/sha256"
	"encoding/hex"
)

// SHA256 returns the hex-encoded SHA-256 of data.
//
// It is always taken on the bytes as uploaded: before image recompression and
// before quarantine wrapping. Both this and Detect answer the same question —
// what did this person actually send us — which is what identifies a sample to
// an analyst and what a chain-of-custody record has to state. A hash of our
// recompressed image, or of our quarantine ZIP, is useless to them.
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
