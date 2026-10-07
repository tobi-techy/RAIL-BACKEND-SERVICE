package di

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	p2pservice "github.com/rail-service/rail_service/internal/domain/services/p2p"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/publictrades"
)

// p2pBillPayerAdapter lets pay-bill automations send real money via P2P.
// Non-Rail payees (email/phone) receive a claim link they can pay out to a bank.
type p2pBillPayerAdapter struct {
	p2p *p2pservice.Service
}

func (a *p2pBillPayerAdapter) SendP2P(ctx context.Context, senderID uuid.UUID, identifier, amount, note, idempotencyKey string) (string, error) {
	resp, err := a.p2p.Send(ctx, senderID, &entities.P2PSendRequest{
		Identifier:     identifier,
		Amount:         amount,
		Note:           note,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Transfer == nil {
		return "", fmt.Errorf("p2p transfer returned no reference")
	}
	return resp.Transfer.ID.String(), nil
}

// publicTradesSourceAdapter bridges the publictrades client into the domain
// copytrading.PublicTradesSource interface.
type publicTradesSourceAdapter struct {
	client *publictrades.Client
}

func (a *publicTradesSourceAdapter) GetFigureTrades(ctx context.Context, figureKey string, since time.Time, limit int) ([]copytrading.PublicTrade, error) {
	trades, err := a.client.GetFigureTrades(ctx, figureKey, since, limit)
	if err != nil {
		return nil, err
	}
	out := make([]copytrading.PublicTrade, 0, len(trades))
	for _, t := range trades {
		out = append(out, copytrading.PublicTrade{
			Ticker:          t.Ticker,
			AssetName:       t.AssetName,
			Side:            t.Side,
			AmountMid:       t.AmountMid,
			AmountRange:     t.AmountRange,
			TransactionDate: t.TransactionDate,
			DisclosureDate:  t.DisclosureDate,
			Ref:             t.Ref,
		})
	}
	return out, nil
}
