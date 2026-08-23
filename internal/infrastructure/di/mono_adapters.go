package di

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
	monosvc "github.com/rail-service/rail_service/internal/domain/services/mono"
	monoadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/mono"
	infrarepos "github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

// initializeMono sets up the Mono adapter, repository, and domain service.
// All Mono features are gated on cfg.Mono.APIKey being non-empty.
func (c *Container) initializeMono() error {
	cfg := c.Config
	if cfg.Mono.APIKey == "" {
		c.ZapLog.Info("Mono not configured (missing API key), open-banking features disabled")
		return nil
	}

	client := monoadapter.NewHTTPClient(monoadapter.Config{
		APIKey:      cfg.Mono.APIKey,
		Environment: cfg.Mono.Environment,
		BaseURL:     cfg.Mono.BaseURL,
		Timeout:     time.Duration(cfg.Mono.Timeout) * time.Second,
		MaxRetries:  cfg.Mono.MaxRetries,
	}, c.ZapLog)

	c.ZapLog.Info("Mono open-banking adapter initialized",
		zap.String("environment", cfg.Mono.Environment),
		zap.String("base_url", cfg.Mono.BaseURL))

	c.MonoRepo = infrarepos.NewMonoRepository(sqlx.NewDb(c.DB, "postgres"))
	c.MonoService = monosvc.NewService(&monoClientAdapter{inner: client}, c.MonoRepo, c.ZapLog)
	c.MonoWebhookHandler = webhooks.NewMonoWebhookHandler(c.MonoService, cfg.Mono.WebhookSecret, c.ZapLog)

	return nil
}

// GetMonoService returns the Mono service, or nil if not configured.
func (c *Container) GetMonoService() *monosvc.Service {
	return c.MonoService
}

// monoClientAdapter adapts the concrete Mono HTTP client to the domain-owned
// monosvc.Client interface, translating between infrastructure DTOs and the
// domain types the Mono service consumes.
type monoClientAdapter struct {
	inner monoadapter.Client
}

var _ monosvc.Client = (*monoClientAdapter)(nil)

func (a *monoClientAdapter) InitiateLinking(ctx context.Context, req *monosvc.LinkingRequest) (string, error) {
	resp, err := a.inner.InitiateLinking(ctx, &monoadapter.InitiateLinkingRequest{
		Customer: monoadapter.Customer{
			Name:  req.CustomerName,
			Email: req.CustomerEmail,
		},
		Meta:        &monoadapter.MetaRef{Ref: req.MetaRef},
		Scope:       "auth",
		RedirectURL: req.RedirectURL,
	})
	if err != nil {
		return "", err
	}
	return resp.RedirectURL, nil
}

func (a *monoClientAdapter) ExchangeCode(ctx context.Context, code string) (*monosvc.AccountInfo, error) {
	resp, err := a.inner.ExchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}
	return &monosvc.AccountInfo{
		ID:            resp.ID,
		Name:          resp.Account.Name,
		AccountNumber: resp.Account.AccountNumber,
		Type:          resp.Account.Type,
	}, nil
}

func (a *monoClientAdapter) GetAccount(ctx context.Context, monoAccountID string) (*monosvc.AccountInfo, error) {
	acct, err := a.inner.GetAccount(ctx, monoAccountID)
	if err != nil {
		return nil, err
	}
	return accountInfoFromAdapter(acct), nil
}

func (a *monoClientAdapter) GetTransactions(ctx context.Context, monoAccountID string, query *monosvc.TransactionQuery) ([]monosvc.Transaction, error) {
	req := &monoadapter.TransactionListQuery{Paginate: false} // get all in one request
	if query != nil {
		req.Start = query.Start.Format("2006-01-02")
		req.End = query.End.Format("2006-01-02")
	}
	txns, err := a.inner.GetTransactions(ctx, monoAccountID, req)
	if err != nil {
		return nil, err
	}

	out := make([]monosvc.Transaction, 0, len(txns))
	for _, t := range txns {
		// Resolve enriched metadata first, falling back to the raw category —
		// same precedence the service previously applied inline.
		category := ""
		subCategory := ""
		if t.Meta != nil {
			category = t.Meta.Category
			subCategory = t.Meta.SubCategory
		}
		if category == "" {
			category = t.Category
		}
		out = append(out, monosvc.Transaction{
			ID:          t.ID,
			Amount:      t.Amount,
			Type:        t.Type,
			Description: t.Description,
			Category:    category,
			SubCategory: subCategory,
			Date:        t.Date,
			Reference:   t.Reference,
		})
	}
	return out, nil
}

func (a *monoClientAdapter) InitiatePayment(ctx context.Context, req *monosvc.PaymentRequest) (*monosvc.PaymentInitiationResult, error) {
	resp, err := a.inner.InitiatePayment(ctx, &monoadapter.InitiatePaymentRequest{
		Amount:      req.AmountKobo,
		Type:        "onetime-debit",
		Method:      "account",
		Account:     req.AccountID,
		Description: req.Description,
		Reference:   req.Reference,
		RedirectURL: req.RedirectURL,
		Customer: &monoadapter.PaymentCustomer{
			Email: req.CustomerEmail,
			Name:  req.CustomerName,
		},
	})
	if err != nil {
		return nil, err
	}
	return &monosvc.PaymentInitiationResult{
		Status:      resp.Status,
		PaymentID:   resp.PaymentID,
		ApprovalURL: resp.ApprovalURL,
	}, nil
}

func (a *monoClientAdapter) VerifyPayment(ctx context.Context, reference string) (*monosvc.PaymentVerification, error) {
	resp, err := a.inner.VerifyPayment(ctx, reference)
	if err != nil {
		return nil, err
	}
	return &monosvc.PaymentVerification{
		Status:  resp.Status,
		MonoRef: resp.MonoRef,
	}, nil
}

func (a *monoClientAdapter) UnlinkAccount(ctx context.Context, monoAccountID string) error {
	return a.inner.UnlinkAccount(ctx, monoAccountID)
}

func accountInfoFromAdapter(acct *monoadapter.Account) *monosvc.AccountInfo {
	return &monosvc.AccountInfo{
		ID:            acct.ID,
		Name:          acct.Name,
		BankName:      acct.BankName,
		AccountNumber: acct.AccountNumber,
		Type:          acct.Type,
		Currency:      acct.Currency,
		Balance:       acct.Balance,
	}
}
