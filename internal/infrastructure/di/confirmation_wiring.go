package di

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	confirmationHandlers "github.com/rail-service/rail_service/internal/api/handlers/confirmation"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	confirmationSvc "github.com/rail-service/rail_service/internal/domain/services/confirmation"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/pkg/auth"
)

// automationRuleUpdater adapts automation.Service.Update to the confirmation
// SaveRuleUpdater seam (same fields, no import weight on the primitive).
type automationRuleUpdater struct {
	svc *automation.Service
}

func (a *automationRuleUpdater) Update(ctx context.Context, userID, id uuid.UUID, req *confirmationSvc.UpdateAutomationFields) (*struct{}, error) {
	if a.svc == nil {
		return nil, errConfirmationNoAutomation
	}
	in := &automation.UpdateAutomationRequest{
		Name: req.Name, Description: req.Description, IsActive: req.IsActive,
		TriggerConfig: req.TriggerConfig, ActionConfig: req.ActionConfig,
	}
	if _, err := a.svc.Update(ctx, userID, id, in); err != nil {
		return nil, err
	}
	return &struct{}{}, nil
}

type confirmationSvcError string

func (e confirmationSvcError) Error() string { return string(e) }

const errConfirmationNoAutomation = confirmationSvcError("automation service not wired (fail-closed)")

// initializeConfirmationServices wires the reusable live confirmation card:
// one primitive for every high-stakes Face ID action.
//
// Must run after initializeDomainServices (P2P, automation, investment) and
// after initializePlatformMessaging (bridge dispatcher for card delivery and
// in-place edits).
func (c *Container) initializeConfirmationServices() {
	cfg := c.Config.Confirmation
	if !cfg.Enabled {
		c.ZapLog.Info("confirmation cards disabled by config (CONFIRMATION_ENABLED=false)")
		return
	}
	if cfg.TokenSecret == "" {
		// Fail closed: without a signing secret no cards are staged and the
		// endpoints report misconfiguration instead of minting unsigned URLs.
		c.ZapLog.Warn("confirmation cards disabled: CONFIRMATION_TOKEN_SECRET not set (fail-closed)")
		return
	}
	if len(cfg.TokenSecret) < 32 {
		c.ZapLog.Error("confirmation cards disabled: CONFIRMATION_TOKEN_SECRET must be >=32 chars (fail-closed)")
		return
	}
	if cfg.RailServiceKey != "" && len(cfg.RailServiceKey) < 32 {
		c.ZapLog.Error("confirmation cards disabled: RAIL_SERVICE_KEY must be >=32 chars when set (fail-closed)")
		return
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = "/confirm"
	}
	ttl := time.Duration(cfg.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = entities.DefaultConfirmationTTL
	}
	svc := confirmationSvc.NewService(confirmationSvc.Config{
		TokenSecret: cfg.TokenSecret,
		ConfirmBase: base,
		TTL:         ttl,
	}, nil, c.Logger)
	// Postgres-backed device keys so restarts/replicas don't silently drop
	// enrollment and fall back to token-only approves. Nil DB (tests) keeps
	// the in-memory store.
	if c.DB != nil {
		svc.SetDeviceStore(confirmationSvc.NewPostgresDeviceStore(c.DB))
	}

	// Same card, different payloads: each action keeps its own backend handler.
	// Cards minted by Miriam for one of its challenges carry
	// payload.miriam_confirm_id and route through the settle executor (any
	// action): Miriam re-checks the challenge binding and executes through
	// GoRail with the confirm_id header, so the existing money gates pass
	// unchanged. App-originated cards carry an app session instead of a
	// challenge and keep their direct executor — they bypass the
	// X-Miriam-Confirm-Id header gate legitimately, because the single-use
	// card token plus Face ID is their authorization.
	miriamBase := strings.TrimSpace(cfg.MiriamBaseURL)
	if miriamBase == "" {
		miriamBase = strings.TrimSpace(c.Config.PythonAgent.BaseURL)
	}
	jwtTTL := c.Config.PythonAgent.JWTTTLSeconds
	if jwtTTL <= 0 {
		jwtTTL = 120
	}
	jwtSecret := c.Config.JWT.Secret
	settle := confirmationSvc.MiriamSettleExecutor(confirmationSvc.MiriamSettleConfig{
		BaseURL:        miriamBase,
		RailServiceKey: cfg.RailServiceKey,
		MintToken: func(ctx context.Context, userID uuid.UUID) (string, error) {
			tok, _, err := auth.GenerateAgentToken(userID, "", "", "user", jwtSecret, jwtTTL)
			return tok, err
		},
	})
	if miriamBase == "" || cfg.RailServiceKey == "" {
		c.ZapLog.Warn("miriam settle path degraded: set RAIL_SERVICE_KEY and the python agent base URL or miriam-originated cards fail closed")
	}
	if c.P2PService != nil {
		svc.RegisterExecutor(entities.ConfirmationActionTransferSend,
			confirmationSvc.RouteByMiriamConfirm(confirmationSvc.TransferSendExecutor(c.P2PService), settle))
	}
	if c.InvestmentGliderService != nil {
		svc.RegisterExecutor(entities.ConfirmationActionInvestBuy,
			confirmationSvc.RouteByMiriamConfirm(confirmationSvc.InvestBuyExecutor(c.InvestmentGliderService), settle))
		svc.RegisterExecutor(entities.ConfirmationActionInvestSell,
			confirmationSvc.RouteByMiriamConfirm(confirmationSvc.InvestSellExecutor(c.InvestmentGliderService), settle))
	}
	if c.AutomationService != nil {
		svc.RegisterExecutor(entities.ConfirmationActionSaveSweep,
			confirmationSvc.RouteByMiriamConfirm(confirmationSvc.SaveSweepExecutor(&automationRuleUpdater{svc: c.AutomationService}), settle))
	}

	// Terminal sync toward Miriam: a Miriam-originated card that goes
	// rejected/expired here first (Face ID cancel, TTL hit) declines/expires
	// the joined challenge. State is already persisted; the callback is
	// fire-and-forget with one retry.
	if miriamBase != "" && cfg.RailServiceKey != "" {
		notifier := &confirmationSvc.TerminalNotifier{BaseURL: miriamBase, RailServiceKey: cfg.RailServiceKey}
		svc.SetTerminalNotifier(func(ctx context.Context, miriamConfirmID, state string) {
			if err := notifier.Notify(ctx, miriamConfirmID, state); err != nil {
				c.ZapLog.Warn("confirmation terminal callback to miriam failed (card state stands)",
					zap.String("confirm_id", miriamConfirmID), zap.String("state", state), zap.Error(err))
			}
		})
	} else {
		c.ZapLog.Warn("confirmation terminal callback to miriam disabled: set RAIL_SERVICE_KEY and the python agent base URL")
	}

	// Device-bound approvals: Secure Enclave keys enrolled trust-on-first-use,
	// signatures required once enrolled. Strict mode (reject token-only
	// outright) stays off until the fleet is enrolled.
	// Hackathon/demo email OTP: when enabled, reject Face ID/token-only approve
	// and require /otp/send + /otp/approve. Force RequireDeviceSignature off so
	// the Face ID path is not simultaneously required.
	// Hackathon flag: Config.DemoEmailOTP (from CONFIRMATION_DEMO_EMAIL_OTP) wins;
	// keep env fallback for operators who toggle without rebuild.
	demoEmailOTP := cfg.DemoEmailOTP
	if !demoEmailOTP {
		if v := os.Getenv("CONFIRMATION_DEMO_EMAIL_OTP"); v == "true" || v == "1" {
			demoEmailOTP = true
		}
	}
	if demoEmailOTP {
		svc.SetDemoEmailOTP(true)
		svc.SetStrictDeviceSignature(false)
		var store confirmationSvc.OTPStore
		if c.RedisClient != nil {
			store = &confirmationSvc.RedisOTPStore{Client: c.RedisClient}
		}
		var mailer confirmationSvc.EmailSender
		if c.EmailService != nil {
			mailer = c.EmailService
		}
		var users confirmationSvc.UserEmailLookup
		if c.UserRepo != nil {
			users = userEmailLookup{repo: c.UserRepo}
		}
		svc.SetEmailOTPDeps(mailer, users, store)
		c.ZapLog.Warn("CONFIRMATION_DEMO_EMAIL_OTP enabled: Face ID approve rejected; email OTP required for money cards")
	} else {
		svc.SetStrictDeviceSignature(cfg.RequireDeviceSignature)
	}
	if d := c.MiriamBridgeDispatcher; d != nil {
		svc.SetCardEditor(func(ctx context.Context, conf *entities.Confirmation) error {
			threadID, _ := conf.Payload["thread_id"].(string)
			return d.SendConfirmationEdit(ctx, conf.UserID, threadID, confirmationCardFor(conf, base))
		})
	}
	svc.SetAuditSink(func(e entities.ConfirmationAuditEntry) {
		c.ZapLog.Info("confirmation transition",
			zap.String("action_id", e.ConfirmationID.String()),
			zap.String("action", string(e.Action)),
			zap.String("from", string(e.FromState)),
			zap.String("to", string(e.ToState)),
			zap.String("biometric", e.Biometric),
			zap.String("amount", e.Amount),
			zap.String("note", e.Note))
	})

	var send confirmationHandlers.CardSender
	if d := c.MiriamBridgeDispatcher; d != nil {
		send = func(ctx context.Context, userID uuid.UUID, threadID string, card *platform.ConfirmationCardPayload) error {
			return d.SendConfirmationCard(ctx, userID, threadID, card)
		}
	}
	c.ConfirmationService = svc
	c.ConfirmationHandlers = confirmationHandlers.NewHandler(svc, send, c.ZapLog)
	c.ZapLog.Info("confirmation cards enabled",
		zap.String("base_url", base), zap.Bool("has_dispatcher", c.MiriamBridgeDispatcher != nil))
}

// confirmationCardFor renders the transcript-edit payload for a confirmation
// in its current state.
func confirmationCardFor(conf *entities.Confirmation, base string) *platform.ConfirmationCardPayload {
	remain := time.Until(conf.ExpiresAt)
	expiresIn := ""
	if remain > 0 {
		expiresIn = formatCountdown(remain)
	}
	layout := entities.PreviewLayoutFor(conf, conf.State, expiresIn)
	return &platform.ConfirmationCardPayload{
		ActionID: conf.ID.String(), Action: string(conf.Action), State: string(conf.State),
		ConfirmURL: strings.TrimRight(base, "/") + "/" + conf.ID.String(),
		Title:      conf.Title, Subtitle: conf.Subtitle,
		Amount: conf.Amount, Asset: conf.Asset, Destination: conf.Destination,
		Fee: conf.Fee, RiskLine: conf.RiskLine,
		ExpiresAt:  conf.ExpiresAt.UTC().Format(time.RFC3339),
		Caption:    layout.Caption,
		Subcaption: layout.Subcaption,
		Image:      layout.Image,
		Summary:    layout.Summary,
	}
}

func formatCountdown(d time.Duration) string {
	total := int(d.Seconds())
	if total < 0 {
		total = 0
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// userEmailLookup adapts UserRepo to the confirmation OTP email seam.
type userEmailLookup struct {
	repo interface {
		GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
	}
}

func (u userEmailLookup) EmailForUser(ctx context.Context, userID uuid.UUID) (string, error) {
	if u.repo == nil {
		return "", fmt.Errorf("user repository not configured")
	}
	profile, err := u.repo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if profile == nil {
		return "", fmt.Errorf("user not found")
	}
	return profile.Email, nil
}
