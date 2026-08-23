package minimax

import (
	"encoding/base64"
	"encoding/hex"
)

// hexDecodeString decodes a hex string to bytes (lower or upper case).
func hexDecodeString(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// base64Decode decodes a standard base64 string.
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
