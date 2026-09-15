// Package investmentowner implements the portfolio-owner authority used by
// enrollment, withdrawal and chain-activation flows.
//
// Glider's two-stage flows require the portfolio owner's signature. The product
// decision for Rail is "Solana, Rail-signed": Rail holds (or controls) the owner
// key so a user can complete an enrollment from chat after explicitly consenting,
// instead of being pushed into an external wallet. That makes Rail the root
// authority over the user's smart accounts even though the assets sit in the
// user's own accounts, so the signer is deliberately small, auditable and
// swappable: replacing it with user-held keys is a configuration change, not a
// rewrite.
package investmentowner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ErrMessageSigningUnsupported is returned when the configured signer has no way
// to produce an ed25519 message signature. Callers must surface this honestly
// instead of falling back to some other key that the smart account does not
// trust.
var ErrMessageSigningUnsupported = errors.New("investment owner: this signer cannot sign messages")

// WalletLookup resolves the user's custody wallet for a chain.
type WalletLookup interface {
	GetWalletByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
}

// signerConfig is the shared configuration for both signer implementations.
type signerConfig struct {
	// AccountPrefix is the CAIP-2 prefix for owner account ids, e.g.
	// "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp".
	accountPrefix string
	chain         entities.WalletChain
}

func newSignerConfig(accountPrefix string, chain entities.WalletChain) signerConfig {
	prefix := strings.TrimSpace(accountPrefix)
	if prefix == "" {
		prefix = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
	}
	return signerConfig{accountPrefix: strings.TrimRight(prefix, ":"), chain: chain}
}

// ownerAccountID builds the CAIP-10 owner account id for an address.
func (c signerConfig) ownerAccountID(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("investment owner: no owner address available")
	}
	if strings.Contains(address, ":") {
		// Already a CAIP-10 id from the provider.
		return address, nil
	}
	return c.accountPrefix + ":" + address, nil
}
