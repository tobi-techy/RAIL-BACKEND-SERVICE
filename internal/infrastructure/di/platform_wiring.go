package di

import (
	"context"
	"fmt"

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

	if c.Config.Platform.Enabled && c.AIOrchestrator != nil {
		platformIdentityRepo := c.PlatformIdentityRepo
		linkingSvc := platform.NewLinkingService(
			platformIdentityRepo,
			c.Config.Platform.HandshakeTokenTTL,
		)
		c.PlatformHandler = platformhandlers.NewPlatformHandler(linkingSvc, c.Config.Platform.BridgeMessagingAddress)

		if c.Config.Platform.AMQPURL != "" {
			userResolver := platform.NewUserResolver(platformIdentityRepo)
			respBuilder := platform.NewResponseBuilder()
			platformOrchestrator := &orchestratorAdapter{
				orchestrator: c.AIOrchestrator,
				convRepo:     c.ConversationRepo,
				deepLinkBase: c.Config.Platform.AppDeepLinkBaseURL,
			}

			var consumer *platform.Consumer

			sendFunc := func(ctx context.Context, msg *platform.OutboundMessage) error {
				data, err := respBuilder.JSON(msg)
				if err != nil {
					return err
				}
				if consumer == nil {
					return fmt.Errorf("platform outbound publisher not ready")
				}
				// Per-platform routing key so multiple bridge consumers (iMessage,
				// Telegram, WhatsApp) never cross-talk. Falls back to the bare key
				// if the platform is somehow unset so the DLQ still catches it.
				routingKey := "message.outbound"
				if msg != nil && msg.Platform != "" {
					routingKey = "message.outbound." + string(msg.Platform)
				}
				if pubErr := consumer.Publish(ctx, c.Config.Platform.AMQPExchange, routingKey, data); pubErr != nil {
					// Surface as an error so the caller does not ack the inbound
					// message; it will be requeued and the reply retried.
					c.ZapLog.Warn("AMQP outbound publish failed", zap.Error(pubErr))
					return fmt.Errorf("publish outbound: %w", pubErr)
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

			// Onboarder needs OnboardingService, wired after domain services init.
			c.platformProcessor = proc
			c.platformLinking = linkingSvc

			var cErr error
			consumer, cErr = platform.NewConsumer(
				c.Config.Platform.AMQPURL,
				c.Config.Platform.AMQPExchange,
				c.Config.Platform.AMQPQueue,
				c.Config.Platform.AMQPRoutingKey,
				proc,
			)
			if cErr != nil {
				c.ZapLog.Warn("Platform consumer init failed (AMQP), platform disabled", zap.Error(cErr))
			} else {
				c.PlatformConsumer = consumer
				if err := consumer.Start(); err != nil {
					c.ZapLog.Warn("Platform consumer start failed", zap.Error(err))
				} else {
					c.ZapLog.Info("Platform messaging consumer started",
						zap.String("exchange", c.Config.Platform.AMQPExchange),
						zap.String("queue", c.Config.Platform.AMQPQueue),
					)
				}
			}

			actionQueue := c.Config.Platform.AMQPActionQueue
			if actionQueue == "" {
				actionQueue = "miriam.actions"
			}
			actionRoutingKey := c.Config.Platform.AMQPActionRoutingKey
			if actionRoutingKey == "" {
				actionRoutingKey = "action.postback"
			}
			actionConsumer, aErr := platform.NewActionConsumer(
				c.Config.Platform.AMQPURL,
				c.Config.Platform.AMQPExchange,
				actionQueue,
				actionRoutingKey,
				proc,
			)
			if aErr != nil {
				c.ZapLog.Warn("Platform action consumer init failed, action postbacks disabled", zap.Error(aErr))
			} else {
				c.PlatformActionConsumer = actionConsumer
				if err := actionConsumer.Start(); err != nil {
					c.ZapLog.Warn("Platform action consumer start failed", zap.Error(err))
				} else {
					c.ZapLog.Info("Platform action consumer started",
						zap.String("exchange", c.Config.Platform.AMQPExchange),
						zap.String("queue", actionQueue),
					)
				}
			}
		}
	}
}
