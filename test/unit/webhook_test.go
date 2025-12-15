package unit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/rail-service/rail_service/pkg/webhook"
	"github.com/stretchr/testify/assert"
)

func TestVerifyWebhookSignature(t *testing.T) {
	payload := []byte(`{"type":"transfer.completed","data":{"id":"transfer_123"}}`)
	secret := "test_webhook_secret"
	
	// Generate valid signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validSig := hex.EncodeToString(mac.Sum(nil))
	
	tests := []struct {
		name      string
		payload   []byte
		signature string
		secret    string
		want      bool
	}{
		{
			name:      "valid signature",
			payload:   payload,
			signature: validSig,
			secret:    secret,
			want:      true,
		},
		{
			name:      "invalid signature",
			payload:   payload,
			signature: "invalid_signature",
			secret:    secret,
			want:      false,
		},
		{
			name:      "wrong secret",
			payload:   payload,
			signature: validSig,
			secret:    "wrong_secret",
			want:      false,
		},
		{
			name:      "modified payload",
			payload:   []byte(`{"type":"transfer.failed"}`),
			signature: validSig,
			secret:    secret,
			want:      false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := webhook.VerifySignature(tt.payload, tt.signature, tt.secret)
			assert.Equal(t, tt.want, result)
		})
	}
}
