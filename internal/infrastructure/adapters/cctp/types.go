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

// FeesResponse represents the fees for a cross-chain transfer
type FeesResponse struct {
	SourceDomain      uint32 `json:"sourceDomain"`
	DestinationDomain uint32 `json:"destinationDomain"`
	FastTransferFee   Fee    `json:"fastTransferFee"`
	StandardFee       Fee    `json:"standardFee"`
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
