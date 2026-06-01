package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(t *testing.T, secret string, body []byte) string {
	t.Helper()

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)

	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerify(t *testing.T) {
	t.Parallel()

	const secret = "topsecret"

	body := []byte(`{"action":"created"}`)
	valid := sign(t, secret, body)

	tests := []struct {
		name      string
		secret    string
		body      []byte
		signature string
		want      bool
	}{
		{name: "valid", secret: secret, body: body, signature: valid, want: true},
		{name: "wrong secret", secret: "other", body: body, signature: valid, want: false},
		{name: "tampered body", secret: secret, body: []byte(`{"action":"deleted"}`), signature: valid, want: false},
		{name: "empty signature", secret: secret, body: body, signature: "", want: false},
		{name: "non-hex signature", secret: secret, body: body, signature: "zzz", want: false},
		{name: "empty secret", secret: "", body: body, signature: valid, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Verify(tt.secret, tt.body, tt.signature); got != tt.want {
				t.Errorf("Verify(%q, ..., %q) = %v, want %v", tt.secret, tt.signature, got, tt.want)
			}
		})
	}
}
