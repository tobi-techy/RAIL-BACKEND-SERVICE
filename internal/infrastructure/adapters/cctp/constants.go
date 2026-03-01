package cctp

import "strings"

const (
	// API Hosts
	IrisMainnetURL = "https://iris-api.circle.com"
	IrisSandboxURL = "https://iris-api-sandbox.circle.com"

	// Domain IDs
	DomainEthereum uint32 = 0
	DomainAvalanche uint32 = 1
	DomainPolygon  uint32 = 7
	DomainSolana   uint32 = 5
	DomainStarknet uint32 = 25

	// Rate limiting
	MaxRequestsPerSecond = 35

	// Attestation statuses
	AttestationStatusPending  = "pending"
	AttestationStatusComplete = "complete"
)

// DomainNames maps domain IDs to human-readable names
var DomainNames = map[uint32]string{
	DomainEthereum:  "Ethereum",
	DomainAvalanche: "Avalanche",
	DomainPolygon:   "Polygon",
	DomainSolana:    "Solana",
	DomainStarknet:  "Starknet",
}

// DomainForChain returns the CCTP domain ID for a given chain identifier.
// Returns (domain, true) if found, (0, false) if unsupported.
func DomainForChain(chain string) (uint32, bool) {
	switch strings.ToUpper(chain) {
	case "ETH", "ETH-SEPOLIA", "ETHEREUM":
		return DomainEthereum, true
	case "AVAX", "AVAX-FUJI", "AVALANCHE":
		return DomainAvalanche, true
	case "MATIC", "MATIC-AMOY", "POLYGON":
		return DomainPolygon, true
	case "SOL", "SOL-DEVNET", "SOLANA":
		return DomainSolana, true
	}
	return 0, false
}
