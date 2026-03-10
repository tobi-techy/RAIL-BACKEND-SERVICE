package cctp

// AttestationResponse represents the response from the attestation API
type AttestationResponse struct {
	Messages []CCTPMessage `json:"messages"`
}

// CCTPMessage represents a single CCTP message with attestation
type CCTPMessage struct {
	Attestation               string `json:"attestation"`
	AttestationStatus         string `json:"attestationStatus"`
	Message                   string `json:"message"`
	MessageHash               string `json:"messageHash"`
	SourceDomain              uint32 `json:"sourceDomain"`
	DestinationDomain         uint32 `json:"destinationDomain"`
	Nonce                     string `json:"nonce"`
	Sender                    string `json:"sender"`
	Recipient                 string `json:"recipient"`
	Amount                    string `json:"amount"`
	FinalityThresholdExecuted uint32 `json:"finalityThresholdExecuted"`
}

// FeesResponse represents the fees for a cross-chain transfer.
// The API returns an array; index 0 is Fast Transfer (finalityThreshold=1000),
// index 1 is Standard Transfer (finalityThreshold=2000).
type FeesResponse struct {
	FastTransferFee Fee
	StandardFee     Fee
}

// FeeEntry is a single entry from the /v2/burn/USDC/fees API response array
type FeeEntry struct {
	FinalityThreshold uint32 `json:"finalityThreshold"`
	MinimumFee        uint64 `json:"minimumFee"` // in basis points (1 = 0.01%)
}

// Fee represents fee details
type Fee struct {
	MinimumFee uint64 `json:"minimumFee"` // in basis points
}

// PublicKeysResponse represents attestation public keys
type PublicKeysResponse struct {
	Keys []PublicKey `json:"keys"`
}

// PublicKey represents a single attestation public key
type PublicKey struct {
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
	Algorithm string `json:"algorithm"`
}
