// Package sign computes and checks the HMAC-SHA-256 signature carried in the HashSHA256 header.
package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

const Header = "HashSHA256"

// Sum returns the HMAC-SHA-256 of data under key as lowercase hex.
func Sum(key, data []byte) string {
	return hex.EncodeToString(mac(key, data))
}

// Valid compares the decoded bytes, not the strings, so the comparison takes constant time and
// uppercase hex of the right MAC passes too.
func Valid(key, data []byte, got string) bool {
	gotMAC, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	return hmac.Equal(gotMAC, mac(key, data))
}

func mac(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}
