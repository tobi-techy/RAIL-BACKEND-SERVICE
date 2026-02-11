package cctp

import "context"

// CCTPClient defines the interface for CCTP Iris API operations
type CCTPClient interface {
	// GetAttestation fetches attestation for a burn transaction
	GetAttestation(ctx context.Context, txHash string) (*AttestationResponse, error)

	// GetFees retrieves current fees for a transfer between domains
	GetFees(ctx context.Context, sourceDomain, destDomain uint32) (*FeesResponse, error)

	// GetPublicKeys retrieves attestation public keys for verification
	GetPublicKeys(ctx context.Context) (*PublicKeysResponse, error)
}

// Ensure Client implements CCTPClient interface
var _ CCTPClient = (*Client)(nil)
