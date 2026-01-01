package cctp

const (
	// API Hosts
	IrisMainnetURL = "https://iris-api.circle.com"
	IrisSandboxURL = "https://iris-api-sandbox.circle.com"

	// Domain IDs - only chains we support
	DomainSolana   uint32 = 5
	DomainPolygon  uint32 = 7
	DomainStarknet uint32 = 25

	// Rate limiting
	MaxRequestsPerSecond = 35

	// Attestation statuses
	AttestationStatusPending  = "pending"
	AttestationStatusComplete = "complete"
)

// DomainNames maps domain IDs to human-readable names
var DomainNames = map[uint32]string{
	DomainSolana:   "Solana",
	DomainPolygon:  "Polygon",
	DomainStarknet: "Starknet",
}
