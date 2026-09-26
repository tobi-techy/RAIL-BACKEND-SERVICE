package di

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	platformhandlers "github.com/rail-service/rail_service/internal/api/handlers/platform"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/document"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

func (c *Container) initializePlatformMessaging() {
	// Initialize platform messaging (iMessage, WhatsApp, Telegram)
	c.PlatformIdentityRepo = repositories.NewPlatformIdentityRepository(c.DB, c.ZapLog)

	pythonReady := c.Config.PythonAgent.Enabled && c.Config.PythonAgent.BaseURL != "" &&
		c.RedisClient != nil && c.Config.JWT.Secret != ""

	// MIRIAM (Python) is the only brain for texted chat. If it is enabled but
	// could not be wired, log loudly so the fail-closed apology users see is
	// deliberate and visible, never silent.
	if c.Config.PythonAgent.Enabled && !pythonReady {
		c.ZapLog.Warn("python agent enabled but not wired — texted chat fails closed to apology (no in-process brain on the messaging channel)",
			zap.Bool("base_url_set", c.Config.PythonAgent.BaseURL != ""),
			zap.Bool("redis_ok", c.RedisClient != nil),
			zap.Bool("jwt_secret_set", c.Config.JWT.Secret != ""),
		)
	}
	// Python-agent delegation is the path that replaces the Go-native AI
	// orchestrator for messaging. It must initialize even when no Cencori key
	// is set (AIOrchestrator stays nil in that case). Require the python
	// client to actually be wirable so we never stand up a processor whose
	// orchestrator AND python client are both nil.
	if c.Config.Platform.Enabled && (c.AIOrchestrator != nil || pythonReady) {
		platformIdentityRepo := c.PlatformIdentityRepo
		linkingSvc := platform.NewLinkingService(
			platformIdentityRepo,
			c.Config.Platform.HandshakeTokenTTL,
		)
		c.PlatformHandler = platformhandlers.NewPlatformHandlerWithLogger(linkingSvc, c.Config.Platform.BridgeMessagingAddress, c.ZapLog)

		if c.Config.Platform.BridgeBaseURL != "" {
			userResolver := platform.NewUserResolver(platformIdentityRepo)
			respBuilder := platform.NewResponseBuilder()
			platformOrchestrator := &orchestratorAdapter{
				orchestrator: c.AIOrchestrator,
				convRepo:     c.ConversationRepo,
				logger:       c.ZapLog,
			}

			// Python-agent delegation: MIRIAM's LLM brain owns messaging chat. Money
			// movements are settled by a confirm_id the Python ledger issued, which
			// is relayed as a Confirm/Cancel poll; Go executes nothing and holds no
			// money state. Requires Redis for the staged id — fail closed to the
			// in-process orchestrator if gone.
			if cfg := c.Config.PythonAgent; cfg.Enabled && cfg.BaseURL != "" && c.RedisClient != nil &&
				c.Config.JWT.Secret != "" {
				c.PythonAgentClient = ai.NewPythonAgentClient(ai.PythonAgentClientConfig{
					BaseURL:            cfg.BaseURL,
					JWTSecret:          c.Config.JWT.Secret,
					JWTTTL:             time.Duration(cfg.JWTTTLSeconds) * time.Second,
					Timeout:            time.Duration(cfg.HTTPTimeoutSeconds) * time.Second,
					NotifyDebitEnabled: cfg.NotifyDebitEnabled,
				}, c.ZapLog)
				platformOrchestrator.python = c.PythonAgentClient
				platformOrchestrator.confirmStore = ai.NewConfirmStore(
					c.RedisClient,
					time.Duration(cfg.ConfirmTTLSeconds)*time.Second,
					c.ZapLog,
				)
				platformOrchestrator.userRepo = c.UserRepo
				c.ZapLog.Info("Platform messaging delegated to Python agent (MIRIAM)",
					zap.String("base_url", cfg.BaseURL),
				)

				// Miriam keeps her own ledger, and it is the only thing her Hands
				// layer checks before a movement — so every genuine credit has to be
				// reported to it, or she will deny a spend the user can plainly
				// afford. Go still credits first; these are projections of committed
				// credits, never the source.
				//
				// The client itself skips non-NGN amounts, because her ledger is
				// single-currency: the rule lives in one place rather than at each
				// call site, where it could be forgotten.
				if c.FundingService != nil {
					c.FundingService.SetInflowNotifier(c.PythonAgentClient)
					c.FundingService.SetDebitNotifier(c.PythonAgentClient)
				}
				if c.GraphVirtualAccountService != nil {
					c.GraphVirtualAccountService.SetInflowNotifier(c.PythonAgentClient)
				}
				if c.P2PService != nil {
					c.P2PService.SetInflowNotifier(c.PythonAgentClient)
					c.P2PService.SetDebitNotifier(c.PythonAgentClient)
				}
				if c.BridgeVirtualAccountService != nil {
					c.BridgeVirtualAccountService.SetInflowNotifier(c.PythonAgentClient)
				}
			}

			bridgeBaseURL := strings.TrimRight(c.Config.Platform.BridgeBaseURL, "/")
			bridgeHMACSecret := c.Config.Platform.BridgeHMACSecret
			bridgeHTTPClient := &http.Client{Timeout: 30 * time.Second}

			// Durable STOP opt-outs. Read on every outbound send below, so a
			// single check covers replies, onboarding sends and unsolicited
			// proactive outreach (MiriamProactiveChatSender is this dispatcher).
			optOutRepo := repositories.NewPlatformOptOutRepository(c.DB, c.ZapLog)

			sendFunc := func(ctx context.Context, msg *platform.OutboundMessage) error {
				// Honour a messaging opt-out. Fail open on a lookup error — the
				// same policy the rest of the app uses for a degraded dependency —
				// because failing closed would silence messaging for everyone
				// during a blip. Logged loudly, since the trade-off is that a blip
				// can let one message reach someone who opted out.
				switch suppressed, err := optOutRepo.IsOptedOut(ctx, string(msg.Platform), msg.UserID); {
				case err != nil:
					c.ZapLog.Error("opt-out check failed on outbound; sending anyway",
						zap.Error(err), zap.String("sender", msg.UserID))
				case suppressed:
					c.ZapLog.Info("suppressed outbound to an opted-out sender",
						zap.String("platform", string(msg.Platform)), zap.String("sender", msg.UserID))
					return nil
				}

				data, err := respBuilder.JSON(msg)
				if err != nil {
					return err
				}
				if bridgeBaseURL == "" {
					return fmt.Errorf("platform bridge URL not configured")
				}

				// HMAC-sign timestamp.nonce.body so the bridge's /send endpoint can
				// verify the request and reject replays.
				timestamp := fmt.Sprintf("%d", time.Now().Unix())
				nonceBytes := make([]byte, 16)
				if _, err := rand.Read(nonceBytes); err != nil {
					return fmt.Errorf("generate bridge nonce: %w", err)
				}
				nonce := hex.EncodeToString(nonceBytes)
				payload := fmt.Sprintf("%s.%s.%s", timestamp, nonce, string(data))
				mac := hmac.New(sha256.New, []byte(bridgeHMACSecret))
				mac.Write([]byte(payload))
				sig := hex.EncodeToString(mac.Sum(nil))

				req, err := http.NewRequestWithContext(ctx, http.MethodPost, bridgeBaseURL+"/send", bytes.NewReader(data))
				if err != nil {
					return fmt.Errorf("create bridge request: %w", err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-HMAC-Timestamp", timestamp)
				req.Header.Set("X-HMAC-Nonce", nonce)
				req.Header.Set("X-HMAC-SHA256", sig)

				resp, err := bridgeHTTPClient.Do(req)
				if err != nil {
					c.ZapLog.Warn("bridge HTTP outbound failed", zap.Error(err))
					return fmt.Errorf("bridge outbound: %w", err)
				}
				defer func() {
					if _, drainErr := io.Copy(io.Discard, resp.Body); drainErr != nil {
						c.ZapLog.Warn("failed to drain bridge response body", zap.Error(drainErr))
					}
					if closeErr := resp.Body.Close(); closeErr != nil {
						c.ZapLog.Warn("failed to close bridge response body", zap.Error(closeErr))
					}
				}()
				if resp.StatusCode >= 300 {
					c.ZapLog.Warn("bridge HTTP outbound returned error", zap.Int("status", resp.StatusCode))
					return fmt.Errorf("bridge outbound: status %d", resp.StatusCode)
				}
				return nil
			}

			bridgeDispatcher := platform.NewBridgeDispatcher(sendFunc, c.ConversationRepo, entities.PlatformIMessage, c.ZapLog)

			// Quiet-hours + daily-frequency guard so Miriam stays a discreet
			// presence, not a notification machine. Timezone resolved per user
			// from their stored country; defaults to Lagos for the core base.
			if c.RedisClient != nil && c.UserRepo != nil {
				userRepo := c.UserRepo
				tzResolver := platform.NewUserTimezoneResolver(func(ctx context.Context, userID uuid.UUID) string {
					u, err := userRepo.GetByID(ctx, userID)
					if err != nil || u == nil || u.Country == nil {
						return ""
					}
					return *u.Country
				})
				guard := platform.NewProactiveGuard(c.RedisClient, tzResolver, "Africa/Lagos", 6, 22, 7, c.ZapLog)
				// Preferences resolver is set after MiriamPreferencesService is
				// constructed (later in Initialize). See wireProactivePreferences.
				c.proactiveGuard = guard
				bridgeDispatcher.SetGuard(guard)
			}

			c.MiriamBridgeDispatcher = bridgeDispatcher
			c.MiriamProactiveChatSender = bridgeDispatcher

			if c.TravelService != nil {
				c.TravelService.SetTicketMessenger(&travelMessengerAdapter{dispatcher: bridgeDispatcher})
			}

			// Voice notes (TTS out / STT in) via ElevenLabs, when configured.
			var voiceTranscoder platform.VoiceTranscoder
			if el := c.Config.AI.ElevenLabs; el.APIKey != "" && el.VoiceID != "" {
				voiceTranscoder = &platformVoiceAdapter{rest: ai.NewElevenLabsREST(ai.ELVoiceConfig{
					APIKey:          el.APIKey,
					VoiceID:         el.VoiceID,
					Stability:       el.Stability,
					SimilarityBoost: el.SimilarityBoost,
					Style:           el.Style,
					UseSpeakerBoost: el.UseSpeakerBoost,
				}, c.ZapLog)}
				c.ZapLog.Info("Platform voice notes enabled (ElevenLabs)")
			}

			proc := platform.NewProcessor(userResolver, platformOrchestrator, respBuilder, linkingSvc, voiceTranscoder, sendFunc)
			proc.SetLogger(c.ZapLog)
			// Inbound turn supersession: when a follow-up lands while a reply is
			// still generating, suppress the older reply instead of shipping a
			// stale answer. Off by default; see docs/miriam-inbound-supersession.md.
			if c.Config.Platform.TurnSupersession && c.RedisClient != nil {
				proc.SetTurnTracker(platform.NewTurnTracker(c.RedisClient, c.ZapLog))
				c.ZapLog.Info("inbound turn supersession enabled")
			}
			// Enables the STOP/START handling in Process. The same store backs the
			// outbound suppression above, so the two halves cannot drift apart.
			proc.SetOptOutStore(optOutRepo)
			// One-time ask for a real address on accounts still carrying the
			// opaque placeholder from a phone-first chat signup.
			proc.SetEmailBackfill(platform.NewAccountReader(c.UserRepo))

			// Receipt photos texted to Miriam: build a lightweight vision pipeline
			// (OCR -> classify -> extract) so she can summarize and offer to log or
			// split. Reuses the same PaddleOCR sidecar + LLM enricher config as the
			// async document worker, but runs synchronously inside the request so the
			// reply arrives in the same conversation turn.
			if docCfg := c.Config.Document; docCfg.EnablePythonOCR && docCfg.OCRServiceURL != "" {
				if ocrEngine := document.NewPythonOCRClient(docCfg.OCRServiceURL, c.ZapLog); ocrEngine != nil {
					var enricher document.Enricher
					if c.Config.AI.Cencori.APIKey != "" {
						enricher = document.NewLLMEnricher(c.Config.AI.Cencori.APIKey, "", "gpt-4o-mini", c.ZapLog)
					}
					visionPipeline := document.NewPipeline(document.PipelineConfig{
						OCR:              ocrEngine,
						Enricher:         enricher,
						MinOCRConfidence: docCfg.MinOCRConfidence,
						Logger:           c.ZapLog,
					})
					proc.SetReceiptVision(platform.NewDocumentReceiptVision(visionPipeline))
					c.ZapLog.Info("Platform receipt vision enabled (PaddleOCR pipeline)")
				}
			}

			// First-login goal seeder: after a successful handshake or chat
			// onboarding, the user gets a 7-step Baby Steps ladder in user_goals
			// so the goal_progress worker has something to track on the next tick.
			// Wired below; the processor + onboarder both need it.
			c.platformProcessor = proc
			c.platformLinking = linkingSvc

			c.ZapLog.Info("Platform messaging via HTTP (bridge)",
				zap.String("bridge_url", bridgeBaseURL),
			)
		}
	}
}

// SetPlatformStatementHandler wires the statement worker pipeline into the
// already-created platform processor and guest onboarder.
func (c *Container) SetPlatformStatementHandler(handler platform.StatementAttachmentHandler) {
	if c.platformProcessor == nil || handler == nil {
		return
	}
	c.platformProcessor.SetStatementAttachmentHandler(handler)
}
