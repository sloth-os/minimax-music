package api

import "encoding/base64"

// stdBase64 returns the standard base64 encoding of b.
func stdBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
