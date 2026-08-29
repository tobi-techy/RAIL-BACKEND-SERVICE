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
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

func (c *Container) initializePlatformMessaging() {
	// Initialize platform messaging (iMessage, WhatsApp, Telegram)
	c.PlatformIdentityRepo = repositories.NewPlatformIdentityRepository(c.DB, c.ZapLog)

	if c.Config.Platform.Enabled && c.AIOrchestrator != nil {
		platformIdentityRepo := c.PlatformIdentityRepo
		linkingSvc := platform.NewLinkingService(
			platformIdentityRepo,
			c.Config.Platform.HandshakeTokenTTL,
		)
		c.PlatformHandler = platformhandlers.NewPlatformHandlerWithLogger(linkingSvc, c.Config.Platform.BridgeMessagingAddress, c.ZapLog)

		if c.Config.Platform.BridgeBaseURL != "" {
			userResolver := platform.NewUserResolver(platformIdentityRepo)
			respBuilder := platform.NewResponseBuilder()
			confirmBase := strings.TrimRight(c.Config.Platform.ConfirmBaseURL, "/")
			if confirmBase == "" {
				confirmBase = "https://app.userail.money"
			}
			platformOrchestrator := &orchestratorAdapter{
				orchestrator: c.AIOrchestrator,
				convRepo:     c.ConversationRepo,
				deepLinkBase: c.Config.Platform.AppDeepLinkBaseURL,
				confirmStore: platform.NewConfirmTokenStore(c.RedisClient, c.ZapLog),
				confirmEmail: c.EmailService,
				userEmail:    adapters.NewUserEmailLookup(c.UserRepo),
				confirmBase:  confirmBase,
			}

			bridgeBaseURL := strings.TrimRight(c.Config.Platform.BridgeBaseURL, "/")
			bridgeHMACSecret := c.Config.Platform.BridgeHMACSecret
			bridgeHTTPClient := &http.Client{Timeout: 30 * time.Second}

			sendFunc := func(ctx context.Context, msg *platform.OutboundMessage) error {
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
			if c.Config.AI.ProactiveVoice {
				bridgeDispatcher.SetComposer(platform.NewProactiveComposer(c.AIProvider, c.ZapLog))
			}

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
			// Effectively-once inbound processing keyed on the bridge's message
			// id — bridge retries must not make Miriam answer twice.
			if c.RedisClient != nil {
				proc.SetInboundDeduper(c.RedisClient)
			}

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

			if c.EmailService != nil && c.UserRepo != nil {
				c.ConfirmHandler = platform.NewConfirmHandler(
					platform.NewConfirmTokenStore(c.RedisClient, c.ZapLog),
					c.AIOrchestrator,
					c.ZapLog,
				)
			}

			c.ZapLog.Info("Platform messaging via HTTP (bridge)",
				zap.String("bridge_url", bridgeBaseURL),
			)
		}
	}
}
