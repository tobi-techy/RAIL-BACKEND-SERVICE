// Package billpay orchestrates Nigerian bill payments (airtime, data,
// electricity, cable TV, betting, transport) through the Airbills gateway.
//
// Settlement reuses Rail's existing money machinery, mirroring the RampHub
// offramp path: the bill's cost in USDC is held on the user's spend balance via
// the double-entry ledger, moved out of the user's Circle wallet to Airbills'
// Solana deposit address (EVM-held USDC bridged via ChainRails), and the
// Airbills transaction is then processed. Any failure reverses the hold; a
// recovery worker reconciles stuck orders.
package billpay

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/airbills"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Bill categories used across the service, tools, and persistence.
const (
	CategoryAirtime     = "airtime"
	CategoryData        = "data"
	CategoryElectricity = "electricity"
	CategoryCable       = "cable"
	CategoryBetting     = "betting"
	CategoryTransport   = "transport"
)

// MandateAll is the sentinel category for a mandate that covers every bill type.
const MandateAll = "all"

// LedgerService for balance checks, holds, and reversals on the spend balance.
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	CreateTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, metadata map[string]interface{}) error
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
}

// CircleTransferAdapter moves USDC out of the user's Circle wallet.
type CircleTransferAdapter interface {
	FindWalletWithUSDC(ctx context.Context, userRefID string) (walletID, tokenID, blockchain, address string, err error)
	TransferUSDCWithIdempotency(ctx context.Context, walletID, tokenID, destinationAddress, amount, idempotencyKey string) (*circlepkg.Transaction, error)
	GetTransaction(ctx context.Context, txID string) (*circlepkg.Transaction, error)
}

// ChainRailsAdapter bridges EVM-held USDC to Airbills' Solana deposit address.
type ChainRailsAdapter interface {
	CreateIntent(ctx context.Context, req *chainrailspkg.CreateIntentRequest) (*chainrailspkg.CreateIntentResponse, error)
}

// CurrencyRateProvider returns the live USD/NGN rate for display and pre-checks.
type CurrencyRateProvider interface {
	GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error)
}

// Notifier sends user-facing push/in-app receipts.
type Notifier interface {
	SendPush(ctx context.Context, userID uuid.UUID, title, message string, data map[string]interface{}) error
}

// Config carries the tunables sourced from AirbillsConfig.
type Config struct {
	DeveloperFeePercent float64
	DefaultToken        string
	MaxAmountNGN        float64
}

// Service is the bill-payment orchestrator.
type Service struct {
	db             *sqlx.DB
	client         *airbills.Client
	ledger         LedgerService
	circleTransfer CircleTransferAdapter
	chainRails     ChainRailsAdapter
	currencyRates  CurrencyRateProvider
	notifier       Notifier
	cfg            Config
	logger         *zap.Logger
}

// NewService builds the bill-payment service. client is required.
func NewService(db *sqlx.DB, client *airbills.Client, cfg Config, logger *zap.Logger) *Service {
	if cfg.DefaultToken == "" {
		cfg.DefaultToken = airbills.TokenUSDC
	}
	return &Service{db: db, client: client, cfg: cfg, logger: logger}
}

func (s *Service) SetLedger(l LedgerService)                 { s.ledger = l }
func (s *Service) SetCircleTransfer(c CircleTransferAdapter) { s.circleTransfer = c }
func (s *Service) SetChainRails(c ChainRailsAdapter)         { s.chainRails = c }
func (s *Service) SetCurrencyRates(c CurrencyRateProvider)   { s.currencyRates = c }
func (s *Service) SetNotifier(n Notifier)                    { s.notifier = n }

// WebhookSecret exposes the configured Airbills callback signing secret.
func (s *Service) WebhookSecret() string { return s.client.WebhookSecret() }

// PayBillRequest is a resolved, ready-to-charge bill payment. The caller (AI
// tool or REST handler) resolves plan/provider IDs and the NGN amount before
// invoking, so this layer only handles money movement and persistence.
type PayBillRequest struct {
	Category       string
	Recipient      string  // phone / meter / smartcard / betting id
	NetworkID      string  // 01..04 for airtime/data (auto-detected if empty)
	ProdID         string  // plan/provider id from a lookup
	ElectID        string  // electricity disco id
	AmountNGN      float64 // face value; required for variable-amount products
	RecipientName  string  // validated meter/account holder name, if known
	BeneficiaryID  *uuid.UUID
	AutomationID   *uuid.UUID
	IdempotencyRef string
}

// PayBillResult summarizes a submitted payment for receipts.
type PayBillResult struct {
	OrderID    uuid.UUID `json:"order_id"`
	AirbillsID string    `json:"airbills_id"`
	Category   string    `json:"category"`
	Recipient  string    `json:"recipient"`
	AmountNGN  float64   `json:"amount_ngn"`
	AmountUSDC string    `json:"amount_usdc"`
	Status     string    `json:"status"`
}

// PayBill charges a user's spend balance for a bill and settles it to Airbills.
func (s *Service) PayBill(ctx context.Context, userID uuid.UUID, req PayBillRequest) (*PayBillResult, error) {
	category := strings.ToLower(strings.TrimSpace(req.Category))
	productCode := categoryToProductCode(category)
	if productCode == "" {
		return nil, fmt.Errorf("unsupported bill category: %q", req.Category)
	}
	if strings.TrimSpace(req.Recipient) == "" {
		return nil, fmt.Errorf("recipient is required")
	}
	if req.AmountNGN <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if s.cfg.MaxAmountNGN > 0 && req.AmountNGN > s.cfg.MaxAmountNGN {
		return nil, fmt.Errorf("amount exceeds the per-payment limit of ₦%.0f", s.cfg.MaxAmountNGN)
	}

	// Reject obvious duplicates within a short window.
	var dup bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM airbills_orders WHERE user_id=$1 AND product_category=$2 AND recipient=$3 AND amount_ngn=$4 AND created_at > NOW() - interval '30 seconds' AND status != 'reversed')`,
		userID, category, req.Recipient, req.AmountNGN).Scan(&dup); err != nil {
		return nil, fmt.Errorf("failed to check for duplicate payment: %w", err)
	}
	if dup {
		return nil, fmt.Errorf("duplicate payment request, please wait a moment")
	}

	token := strings.ToUpper(strings.TrimSpace(s.cfg.DefaultToken))
	if token == "" {
		token = airbills.TokenUSDC
	}

	// Auto-detect the mobile network for airtime/data when not supplied.
	networkID := req.NetworkID
	if networkID == "" && (category == CategoryAirtime || category == CategoryData) {
		if net, derr := s.client.DetectNetwork(ctx, req.Recipient); derr == nil && net.Data.NetworkID != "" {
			networkID = net.Data.NetworkID
		}
	}

	// Cheap pre-check: refuse clearly-insufficient balances before creating an
	// Airbills transaction. The authoritative hold happens on amountInToken.
	var rate decimal.Decimal
	if s.ledger != nil && s.currencyRates != nil {
		if r, rerr := s.currencyRates.GetLatestRate(ctx, "USD", "NGN"); rerr == nil && r.IsPositive() {
			rate = r
			est := decimal.NewFromFloat(req.AmountNGN).Div(rate).Mul(decimal.NewFromFloat(1.05))
			if bal, berr := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance); berr == nil && bal.LessThan(est) {
				return nil, fmt.Errorf("insufficient balance for this payment")
			}
		}
	}

	// Create the Airbills transaction to obtain the deposit address + exact
	// USDC cost. No funds have moved yet.
	tx, err := s.client.Transact(ctx, airbills.TransactRequest{
		ProductCode: productCode,
		PayWith:     airbills.PayWithTransfer,
		Data: airbills.TransactData{
			Token:       token,
			Amount:      req.AmountNGN,
			PhoneNumber: phoneFor(category, req.Recipient),
			NetworkID:   networkID,
			ProdID:      req.ProdID,
			MeterNo:     meterFor(category, req.Recipient),
			ElectID:     req.ElectID,
			SmartCardNo: smartcardFor(category, req.Recipient),
			CustomerID:  customerFor(category, req.Recipient),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("bill provider could not start this payment: %w", err)
	}
	if tx.Wallet == "" || tx.AmountInToken <= 0 {
		return nil, fmt.Errorf("bill provider returned an invalid quote")
	}

	amountInToken := decimal.NewFromFloat(tx.AmountInToken).Round(6)
	railFee := amountInToken.Mul(decimal.NewFromFloat(s.cfg.DeveloperFeePercent / 100)).Round(6)
	totalHold := amountInToken.Add(railFee)

	// Balance check + hold on the spend balance.
	if s.ledger != nil {
		balance, berr := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		if berr != nil {
			return nil, fmt.Errorf("failed to check balance: %w", berr)
		}
		if balance.LessThan(totalHold) {
			return nil, fmt.Errorf("insufficient balance: have %s USDC, need ~%s USDC for ₦%.0f",
				balance.StringFixed(2), totalHold.StringFixed(2), req.AmountNGN)
		}
		if herr := s.ledger.CreateTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
			entities.TransactionTypeWithdrawal, totalHold, map[string]interface{}{
				"provider": "airbills", "type": "billpay_hold", "category": category,
				"amount_ngn": req.AmountNGN, "airbills_id": tx.ID, "rail_fee": railFee.String(),
			}); herr != nil {
			return nil, fmt.Errorf("failed to reserve funds: %w", herr)
		}
	}

	orderID := uuid.New()
	rateValue := interface{}(nil)
	if rate.IsPositive() {
		rateValue = rate
	}
	if _, dbErr := s.db.ExecContext(ctx, `
		INSERT INTO airbills_orders (id, user_id, airbills_id, request_reference, product_code, product_category, status, beneficiary_id, recipient, network_id, prod_id, recipient_name, amount_ngn, token, amount_in_token, rail_fee_usdc, hold_amount, rate, deposit_address, automation_id)
		VALUES ($1,$2,$3,$4,$5,$6,'held',$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		orderID, userID, tx.ID, nullStr(req.IdempotencyRef), productCode, category,
		req.BeneficiaryID, req.Recipient, nullStr(networkID), nullStr(req.ProdID), nullStr(req.RecipientName),
		req.AmountNGN, token, amountInToken, railFee, totalHold, rateValue, tx.Wallet, req.AutomationID); dbErr != nil {
		s.logger.Error("CRITICAL: failed to persist airbills order — reversing hold", zap.Error(dbErr), zap.String("airbills_id", tx.ID))
		s.reverseHold(ctx, userID, tx.ID, totalHold, railFee, "db_fail")
		return nil, fmt.Errorf("failed to record payment")
	}
	if req.BeneficiaryID != nil {
		// Updating last_used_at is best-effort: it affects only UX sorting/recommendations
		// and must never fail a payment that has already been recorded. A warning log
		// provides the hook for monitoring; the order record itself is the audit source.
		if _, err := s.db.ExecContext(ctx, `UPDATE bill_beneficiaries SET last_used_at=NOW() WHERE id=$1 AND user_id=$2`, req.BeneficiaryID, userID); err != nil {
			s.logger.Warn("failed to update beneficiary last_used_at", zap.Error(err), zap.String("beneficiary_id", req.BeneficiaryID.String()))
		}
	}

	if s.circleTransfer == nil {
		s.logger.Error("Circle transfer adapter not configured for airbills — reversing hold", zap.String("airbills_id", tx.ID))
		s.reverseHold(ctx, userID, tx.ID, totalHold, railFee, "no_transfer_config")
		return nil, fmt.Errorf("payment infrastructure not available")
	}

	// The transfer + process must outlive the HTTP request.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("CRITICAL: panic in airbills settlement goroutine",
					zap.String("airbills_id", tx.ID), zap.Any("panic", r), zap.Stack("stack"))
				s.reverseHold(context.Background(), userID, tx.ID, totalHold, railFee, "settle_panic")
			}
		}()
		s.settle(settleCtx, userID, tx.ID, productCode, tx.Wallet, amountInToken, totalHold, railFee)
	}()

	return &PayBillResult{
		OrderID: orderID, AirbillsID: tx.ID, Category: category, Recipient: req.Recipient,
		AmountNGN: req.AmountNGN, AmountUSDC: amountInToken.StringFixed(2), Status: "processing",
	}, nil
}

// PayUtilityBill adapts PayBill to the automation UtilityBillPayer interface.
// It returns the Airbills transaction id and the USDC amount for the receipt.
func (s *Service) PayUtilityBill(ctx context.Context, userID, automationID uuid.UUID, category, recipient, networkID, prodID, electID string, amountNGN float64, beneficiaryID *uuid.UUID) (string, string, error) {
	res, err := s.PayBill(ctx, userID, PayBillRequest{
		Category:      category,
		Recipient:     recipient,
		NetworkID:     networkID,
		ProdID:        prodID,
		ElectID:       electID,
		AmountNGN:     amountNGN,
		BeneficiaryID: beneficiaryID,
		AutomationID:  &automationID,
	})
	if err != nil {
		return "", "", err
	}
	return res.AirbillsID, res.AmountUSDC, nil
}

// GetPaymentHistory returns a user's recent bill payments.
func (s *Service) GetPaymentHistory(ctx context.Context, userID uuid.UUID, limit int) ([]BillPayment, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, product_category, recipient, amount_ngn, amount_in_token, status, created_at
		FROM airbills_orders WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BillPayment
	for rows.Next() {
		var p BillPayment
		var usdc sql.NullString
		if err := rows.Scan(&p.ID, &p.Category, &p.Recipient, &p.AmountNGN, &usdc, &p.Status, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.AmountUSDC = usdc.String
		out = append(out, p)
	}
	return out, rows.Err()
}

// BillPayment is a single history row.
type BillPayment struct {
	ID         uuid.UUID `json:"id"`
	Category   string    `json:"category"`
	Recipient  string    `json:"recipient"`
	AmountNGN  float64   `json:"amount_ngn"`
	AmountUSDC string    `json:"amount_usdc"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// OrderDetail is the full view of a single Airbills order.
type OrderDetail struct {
	ID               uuid.UUID  `json:"id"`
	AirbillsID       string     `json:"airbills_id"`
	Category         string     `json:"category"`
	Recipient        string     `json:"recipient"`
	RecipientName    string     `json:"recipient_name,omitempty"`
	NetworkID        string     `json:"network_id,omitempty"`
	ProdID           string     `json:"prod_id,omitempty"`
	ElectID          string     `json:"elect_id,omitempty"`
	AmountNGN        float64    `json:"amount_ngn"`
	AmountUSDC       string     `json:"amount_usdc"`
	RailFeeUSDC      string     `json:"rail_fee_usdc"`
	Rate             float64    `json:"rate,omitempty"`
	Token            string     `json:"token"`
	Status           string     `json:"status"`
	FailureReason    string     `json:"failure_reason,omitempty"`
	BridgeTransferID string     `json:"bridge_transfer_id,omitempty"`
	BeneficiaryID    *uuid.UUID `json:"beneficiary_id,omitempty"`
	AutomationID     *uuid.UUID `json:"automation_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// GetOrder returns a single Airbills order if it belongs to the user.
func (s *Service) GetOrder(ctx context.Context, userID, orderID uuid.UUID) (*OrderDetail, error) {
	var o OrderDetail
	var usdc, fee, rate, failureReason sql.NullString
	var bridgeID, beneficiaryID, automationID sql.NullString
	var recipientName, networkID, prodID, electID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, airbills_id, product_category, recipient, recipient_name, network_id, prod_id, elect_id,
		       amount_ngn, amount_in_token, rail_fee_usdc, rate, token, status, failure_reason,
		       bridge_transfer_id, beneficiary_id, automation_id, created_at, updated_at
		FROM airbills_orders WHERE id=$1 AND user_id=$2`, orderID, userID).Scan(
		&o.ID, &o.AirbillsID, &o.Category, &o.Recipient, &recipientName, &networkID, &prodID, &electID,
		&o.AmountNGN, &usdc, &fee, &rate, &o.Token, &o.Status, &failureReason,
		&bridgeID, &beneficiaryID, &automationID, &o.CreatedAt, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	o.AmountUSDC = usdc.String
	o.RailFeeUSDC = fee.String
	if rate.Valid {
		if r, err := strconv.ParseFloat(rate.String, 64); err == nil {
			o.Rate = r
		}
	}
	o.FailureReason = failureReason.String
	o.RecipientName = recipientName.String
	o.NetworkID = networkID.String
	o.ProdID = prodID.String
	o.ElectID = electID.String
	o.BridgeTransferID = bridgeID.String
	if beneficiaryID.Valid {
		if id, err := uuid.Parse(beneficiaryID.String); err == nil {
			o.BeneficiaryID = &id
		}
	}
	if automationID.Valid {
		if id, err := uuid.Parse(automationID.String); err == nil {
			o.AutomationID = &id
		}
	}
	return &o, nil
}

// --- Lookups (pass-through to Airbills for the AI tools) ---

// ListDataPlans returns data bundles, optionally scoped to a network.
func (s *Service) ListDataPlans(ctx context.Context, networkID string) ([]airbills.Product, error) {
	return s.client.ListProducts(ctx, "internet", networkID)
}

// ListCablePackages returns cable-TV bouquets.
func (s *Service) ListCablePackages(ctx context.Context) ([]airbills.Product, error) {
	return s.client.ListProducts(ctx, "cable", "")
}

// ListElectricityDiscos returns electricity providers (discos).
func (s *Service) ListElectricityDiscos(ctx context.Context) ([]airbills.Product, error) {
	return s.client.ListProducts(ctx, "elect", "")
}

// ListBettingProviders returns supported betting platforms.
func (s *Service) ListBettingProviders(ctx context.Context) ([]airbills.Product, error) {
	return s.client.ListProducts(ctx, "bet", "")
}

// ListTransportProviders returns supported transport providers.
func (s *Service) ListTransportProviders(ctx context.Context) ([]airbills.Product, error) {
	return s.client.ListProducts(ctx, "transport", "")
}

// DetectNetwork resolves the mobile network for a phone number.
func (s *Service) DetectNetwork(ctx context.Context, phone string) (string, string, error) {
	res, err := s.client.DetectNetwork(ctx, phone)
	if err != nil {
		return "", "", err
	}
	return res.Data.NetworkID, res.Data.Network, nil
}

// ValidateMeter confirms a meter number and returns the account holder name.
func (s *Service) ValidateMeter(ctx context.Context, meterNo, electID string) (string, error) {
	res, err := s.client.ValidateMeter(ctx, meterNo, electID)
	if err != nil {
		return "", err
	}
	return res.Data.AccountName, nil
}

// --- helpers ---

func categoryToProductCode(category string) string {
	switch category {
	case CategoryAirtime:
		return airbills.ProductAirtime
	case CategoryElectricity:
		return airbills.ProductElectricity
	case CategoryData:
		return airbills.ProductData
	case CategoryBetting:
		return airbills.ProductBetting
	case CategoryCable:
		return airbills.ProductCableTV
	case CategoryTransport:
		return airbills.ProductTransport
	default:
		return ""
	}
}

// The recipient field maps to a different Airbills data field per category.
func phoneFor(category, recipient string) string {
	if category == CategoryAirtime || category == CategoryData {
		return recipient
	}
	return ""
}

func meterFor(category, recipient string) string {
	if category == CategoryElectricity {
		return recipient
	}
	return ""
}

func smartcardFor(category, recipient string) string {
	if category == CategoryCable {
		return recipient
	}
	return ""
}

func customerFor(category, recipient string) string {
	if category == CategoryBetting || category == CategoryTransport {
		return recipient
	}
	return ""
}

func nullStr(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// IsCategorySupported reports whether category is a known bill type.
func IsCategorySupported(category string) bool {
	return categoryToProductCode(strings.ToLower(strings.TrimSpace(category))) != ""
}
