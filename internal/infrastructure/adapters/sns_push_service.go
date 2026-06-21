package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

// SNSPushConfig holds the platform application ARNs for each platform
type SNSPushConfig struct {
	Region         string // AWS region
	IOSPlatformARN string // SNS Platform Application ARN for APNs (iOS)
	AndroidPlatformARN string // SNS Platform Application ARN for FCM (Android)
}

// SNSPushService sends push notifications via AWS SNS.
// Expo push tokens are routed through an optional ExpoPushService fallback.
// Implements the PushSender interface (SendToUser).
type SNSPushService struct {
	client       *sns.Client
	tokenRepo    *repositories.DeviceTokenRepository
	config       SNSPushConfig
	logger       *zap.Logger
	expoFallback *ExpoPushService // handles ExponentPushToken / ExpoPushToken tokens
}

// NewSNSPushService creates a new SNS push service.
func NewSNSPushService(ctx context.Context, cfg SNSPushConfig, tokenRepo *repositories.DeviceTokenRepository, logger *zap.Logger) (*SNSPushService, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	// Verify credentials are actually available (fails fast on non-AWS environments).
	creds, err := awsCfg.Credentials.Retrieve(ctx)
	if err != nil || creds.AccessKeyID == "" {
		return nil, fmt.Errorf("no AWS credentials available for SNS: %w", err)
	}
	return &SNSPushService{
		client:    sns.NewFromConfig(awsCfg),
		tokenRepo: tokenRepo,
		config:    cfg,
		logger:    logger,
	}, nil
}

// SetExpoFallback wires an Expo push service for tokens that SNS cannot handle.
func (s *SNSPushService) SetExpoFallback(expo *ExpoPushService) { s.expoFallback = expo }

// SendToUser sends a push notification to all of a user's devices via SNS.
// Expo push tokens are forwarded to the Expo push service if one is configured.
func (s *SNSPushService) SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error {
	tokens, err := s.tokenRepo.GetUserTokens(ctx, userID)
	if err != nil {
		return fmt.Errorf("get device tokens: %w", err)
	}
	if len(tokens) == 0 {
		s.logger.Debug("No device tokens for user", zap.String("user_id", userID.String()))
		return nil
	}

	// Convert data to map[string]string for payload
	strData := make(map[string]string, len(data))
	for k, v := range data {
		switch val := v.(type) {
		case string:
			strData[k] = val
		default:
			b, _ := json.Marshal(val)
			strData[k] = string(b)
		}
	}

	var successCount, failCount int
	for _, dt := range tokens {
		// Route Expo tokens through the Expo push service.
		if strings.HasPrefix(dt.Token, "ExponentPushToken[") || strings.HasPrefix(dt.Token, "ExpoPushToken[") {
			if s.expoFallback != nil {
				if err := s.expoFallback.SendToUser(ctx, userID, title, body, data); err != nil {
					s.logger.Warn("Expo push fallback failed", zap.Error(err), zap.String("user_id", userID.String()))
					failCount++
				} else {
					successCount++
				}
			} else {
				s.logger.Warn("Expo push token received but no Expo fallback configured — skipping",
					zap.String("user_id", userID.String()))
				failCount++
			}
			continue
		}

		// Ensure we have an SNS endpoint ARN for this token
		endpointARN := dt.EndpointARN
		if endpointARN == nil || *endpointARN == "" {
			arn, err := s.ensureEndpoint(ctx, dt)
			if err != nil {
				s.logger.Warn("Failed to create SNS endpoint", zap.Error(err))
				failCount++
				continue
			}
			endpointARN = &arn
		}

		if err := s.publishToEndpoint(ctx, *endpointARN, dt.Platform, title, body, strData); err != nil {
			s.logger.Warn("SNS push failed",
				zap.String("token_prefix", truncateToken(dt.Token)),
				zap.Error(err))
			// Deactivate endpoint if it's disabled/invalid
			if isEndpointDisabled(err) {
				s.tokenRepo.DeleteToken(ctx, dt.Token)
				s.logger.Info("Deactivated stale device token", zap.String("token_prefix", truncateToken(dt.Token)))
			}
			failCount++
			continue
		}
		successCount++
	}

	s.logger.Info("SNS push sent",
		zap.Int("total", len(tokens)),
		zap.Int("success", successCount),
		zap.Int("failure", failCount))
	return nil
}

// ensureEndpoint creates an SNS platform endpoint for a device token and stores the ARN.
func (s *SNSPushService) ensureEndpoint(ctx context.Context, dt *repositories.DeviceToken) (string, error) {
	platformARN := s.platformARNForDevice(dt.Platform)
	if platformARN == "" {
		return "", fmt.Errorf("no platform ARN configured for platform: %s", dt.Platform)
	}

	result, err := s.client.CreatePlatformEndpoint(ctx, &sns.CreatePlatformEndpointInput{
		PlatformApplicationArn: aws.String(platformARN),
		Token:                  aws.String(dt.Token),
		CustomUserData:         aws.String(dt.UserID.String()),
	})
	if err != nil {
		return "", fmt.Errorf("create platform endpoint: %w", err)
	}

	arn := *result.EndpointArn
	// Persist the ARN so we don't recreate it next time
	if err := s.tokenRepo.UpdateEndpointARN(ctx, dt.ID, arn); err != nil {
		s.logger.Warn("Failed to persist endpoint ARN", zap.Error(err))
	}
	return arn, nil
}

func (s *SNSPushService) platformARNForDevice(platform string) string {
	switch strings.ToLower(platform) {
	case "ios", "apns":
		return s.config.IOSPlatformARN
	case "android", "fcm", "gcm":
		return s.config.AndroidPlatformARN
	default:
		return ""
	}
}

// publishToEndpoint sends a push notification to a specific SNS endpoint.
func (s *SNSPushService) publishToEndpoint(ctx context.Context, endpointARN, platform, title, body string, data map[string]string) error {
	payload := s.buildPayload(platform, title, body, data)
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	_, err = s.client.Publish(ctx, &sns.PublishInput{
		TargetArn:        aws.String(endpointARN),
		Message:          aws.String(string(jsonPayload)),
		MessageStructure: aws.String("json"),
	})
	return err
}

// buildPayload creates the platform-specific SNS message structure.
func (s *SNSPushService) buildPayload(platform, title, body string, data map[string]string) map[string]string {
	// FCM/Android payload
	gcmPayload, _ := json.Marshal(map[string]interface{}{
		"notification": map[string]string{"title": title, "body": body, "sound": "default"},
		"data":         data,
		"priority":     "high",
	})

	// APNs/iOS payload
	apnsPayload, _ := json.Marshal(map[string]interface{}{
		"aps": map[string]interface{}{
			"alert": map[string]string{"title": title, "body": body},
			"sound": "default",
		},
		"data": data,
	})

	return map[string]string{
		"default": body,
		"GCM":     string(gcmPayload),
		"APNS":    string(apnsPayload),
		"APNS_SANDBOX": string(apnsPayload),
	}
}

// isEndpointDisabled checks if the SNS error indicates a disabled/deleted endpoint.
func isEndpointDisabled(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EndpointDisabled") ||
		strings.Contains(msg, "InvalidParameter") ||
		strings.Contains(msg, "NotFound")
}
