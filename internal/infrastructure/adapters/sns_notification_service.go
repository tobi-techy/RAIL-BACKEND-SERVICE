package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SNSConfig holds AWS SNS configuration
type SNSConfig struct {
	Region              string
	PushPlatformARN     string // Platform application ARN for push
	SMSTopicARN         string // Topic ARN for SMS
	EmailTopicARN       string // Topic ARN for email
	NotificationQueueURL string // SQS queue for async processing
}

// SNSNotificationService implements notification delivery via AWS SNS/SQS
type SNSNotificationService struct {
	snsClient *sns.Client
	sqsClient *sqs.Client
	config    SNSConfig
	logger    *zap.Logger
}

// NewSNSNotificationService creates a new SNS notification service
func NewSNSNotificationService(ctx context.Context, cfg SNSConfig, logger *zap.Logger) (*SNSNotificationService, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &SNSNotificationService{
		snsClient: sns.NewFromConfig(awsCfg),
		sqsClient: sqs.NewFromConfig(awsCfg),
		config:    cfg,
		logger:    logger,
	}, nil
}

// NotificationMessage represents a notification to be sent
type NotificationMessage struct {
	UserID    uuid.UUID         `json:"user_id"`
	Type      string            `json:"type"` // push, sms, email
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Data      map[string]string `json:"data,omitempty"`
	Priority  string            `json:"priority"` // high, normal
	Recipient string            `json:"recipient,omitempty"` // phone/email/device token
}

// SendPush sends a push notification via SNS
func (s *SNSNotificationService) SendPush(ctx context.Context, endpointARN string, title, body string, data map[string]string) error {
	payload := map[string]interface{}{
		"default": body,
		"GCM": map[string]interface{}{
			"notification": map[string]string{"title": title, "body": body},
			"data":         data,
		},
		"APNS": map[string]interface{}{
			"aps": map[string]interface{}{"alert": map[string]string{"title": title, "body": body}, "sound": "default"},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal push payload: %w", err)
	}

	_, err = s.snsClient.Publish(ctx, &sns.PublishInput{
		TargetArn:        aws.String(endpointARN),
		Message:          aws.String(string(jsonPayload)),
		MessageStructure: aws.String("json"),
	})
	if err != nil {
		s.logger.Error("Failed to send push via SNS", zap.Error(err))
		return fmt.Errorf("SNS publish failed: %w", err)
	}

	s.logger.Info("Push notification sent", zap.String("endpoint", endpointARN[:20]+"..."))
	return nil
}

// SendSMS sends an SMS via SNS
func (s *SNSNotificationService) SendSMS(ctx context.Context, phoneNumber, message string) error {
	_, err := s.snsClient.Publish(ctx, &sns.PublishInput{
		PhoneNumber: aws.String(phoneNumber),
		Message:     aws.String(message),
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"AWS.SNS.SMS.SMSType": {DataType: aws.String("String"), StringValue: aws.String("Transactional")},
		},
	})
	if err != nil {
		s.logger.Error("Failed to send SMS via SNS", zap.Error(err), zap.String("phone", s.maskPhone(phoneNumber)))
		return fmt.Errorf("SNS SMS failed: %w", err)
	}

	s.logger.Info("SMS sent", zap.String("phone", s.maskPhone(phoneNumber)))
	return nil
}

// QueueNotification queues a notification for async processing via SQS
func (s *SNSNotificationService) QueueNotification(ctx context.Context, msg *NotificationMessage) error {
	if s.config.NotificationQueueURL == "" {
		return s.processNotificationSync(ctx, msg)
	}

	jsonMsg, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	_, err = s.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(s.config.NotificationQueueURL),
		MessageBody: aws.String(string(jsonMsg)),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"Type": {DataType: aws.String("String"), StringValue: aws.String(msg.Type)},
		},
	})
	if err != nil {
		s.logger.Error("Failed to queue notification", zap.Error(err))
		return fmt.Errorf("SQS send failed: %w", err)
	}

	s.logger.Debug("Notification queued", zap.String("type", msg.Type), zap.String("user_id", msg.UserID.String()))
	return nil
}

// processNotificationSync processes notification synchronously (fallback)
func (s *SNSNotificationService) processNotificationSync(ctx context.Context, msg *NotificationMessage) error {
	switch msg.Type {
	case "sms":
		return s.SendSMS(ctx, msg.Recipient, msg.Body)
	case "push":
		return s.SendPush(ctx, msg.Recipient, msg.Title, msg.Body, msg.Data)
	default:
		s.logger.Debug("Notification type not handled synchronously", zap.String("type", msg.Type))
		return nil
	}
}

// RegisterDeviceToken registers a device token with SNS platform application
func (s *SNSNotificationService) RegisterDeviceToken(ctx context.Context, userID uuid.UUID, token, platform string) (string, error) {
	if s.config.PushPlatformARN == "" {
		return "", fmt.Errorf("push platform ARN not configured")
	}

	result, err := s.snsClient.CreatePlatformEndpoint(ctx, &sns.CreatePlatformEndpointInput{
		PlatformApplicationArn: aws.String(s.config.PushPlatformARN),
		Token:                  aws.String(token),
		CustomUserData:         aws.String(userID.String()),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create platform endpoint: %w", err)
	}

	s.logger.Info("Device registered for push", zap.String("user_id", userID.String()))
	return *result.EndpointArn, nil
}

func (s *SNSNotificationService) maskPhone(phone string) string {
	if len(phone) < 7 {
		return "****"
	}
	return phone[:3] + "****" + phone[len(phone)-3:]
}

// HealthCheck verifies SNS connectivity
func (s *SNSNotificationService) HealthCheck(ctx context.Context) error {
	_, err := s.snsClient.ListTopics(ctx, &sns.ListTopicsInput{})
	return err
}
