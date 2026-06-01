package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Verify reports whether hexSignature is a valid HMAC-SHA256 of body keyed by
// secret. Gitea signs each delivery body with the webhook secret and sends the
// hex-encoded digest in a header; this signature is the only trust boundary for
// inbound events, so the comparison is constant-time. A malformed hex signature
// or an empty secret yields false.
func Verify(secret string, body []byte, hexSignature string) bool {
	if secret == "" {
		return false
	}

	want, err := hex.DecodeString(hexSignature)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)

	return hmac.Equal(want, mac.Sum(nil))
}
