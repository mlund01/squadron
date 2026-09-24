package store

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
)

func generateID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// MissionIDForLaunch returns the stable child-run identity for one durable
// parent launch request.
func MissionIDForLaunch(parentRunID, launchID string) string {
	digest := sha256.Sum256([]byte(parentRunID + "\x00" + launchID))
	return "route_" + hex.EncodeToString(digest[:12])
}
