package notification_worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NotificationMessage represents a queued notification
type NotificationMessage struct {
	UserID    uuid.UUID         `json:"user_id"`
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Data      map[string]string `json:"data,omitempty"`
	Priority  string            `json:"priority"`
	Recipient string            `json:"recipient,omitempty"`
}

// Sender defines notification sending operations
type Sender interface {
	SendPush(ctx context.Context, endpointARN, title, body string, data map[string]string) error
	SendSMS(ctx context.Context, phone, message string) error
}

// EmailSender defines email sending operations
type EmailSender interface {
	SendEmail(ctx context.Context, to, subject, body string) error
}

// DeviceRepo retrieves user device tokens
type DeviceRepo interface {
	GetUserDeviceTokens(ctx context.Context, userID uuid.UUID) ([]string, error)
	GetUserPhone(ctx context.Context, userID uuid.UUID) (string, error)
	GetUserEmail(ctx context.Context, userID uuid.UUID) (string, error)
}

// Worker processes notifications from SQS queue
type Worker struct {
	sqsClient   *sqs.Client
	queueURL    string
	sender      Sender
	emailSender EmailSender
	deviceRepo  DeviceRepo
	logger      *zap.Logger
	stopCh      chan struct{}
}

// Config holds worker configuration
type Config struct {
	Region   string
	QueueURL string
}

// NewWorker creates a new notification worker
func NewWorker(ctx context.Context, cfg Config, sender Sender, emailSender EmailSender, deviceRepo DeviceRepo, logger *zap.Logger) (*Worker, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Worker{
		sqsClient:   sqs.NewFromConfig(awsCfg),
		queueURL:    cfg.QueueURL,
		sender:      sender,
		emailSender: emailSender,
		deviceRepo:  deviceRepo,
		logger:      logger,
		stopCh:      make(chan struct{}),
	}, nil
}

// Start begins processing notifications from the queue
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting notification worker", zap.String("queue", w.queueURL))

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Notification worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("Notification worker stopped")
			return
		default:
			w.pollAndProcess(ctx)
		}
	}
}

// Stop stops the worker
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) pollAndProcess(ctx context.Context) {
	result, err := w.sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(w.queueURL),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     20,
		VisibilityTimeout:   30,
	})
	if err != nil {
		w.logger.Error("Failed to receive messages", zap.Error(err))
		time.Sleep(5 * time.Second)
		return
	}

	for _, msg := range result.Messages {
		if err := w.processMessage(ctx, msg.Body, msg.ReceiptHandle); err != nil {
			w.logger.Error("Failed to process notification", zap.Error(err))
			continue
		}

		// Delete processed message
		_, _ = w.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
			QueueUrl:      aws.String(w.queueURL),
			ReceiptHandle: msg.ReceiptHandle,
		})
	}
}

func (w *Worker) processMessage(ctx context.Context, body *string, receiptHandle *string) error {
	if body == nil {
		return fmt.Errorf("empty message body")
	}

	var msg NotificationMessage
	if err := json.Unmarshal([]byte(*body), &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %w", err)
	}

	w.logger.Debug("Processing notification",
		zap.String("type", msg.Type),
		zap.String("user_id", msg.UserID.String()))

	switch msg.Type {
	case "push":
		return w.sendPushNotification(ctx, &msg)
	case "sms":
		return w.sendSMSNotification(ctx, &msg)
	case "email":
		return w.sendEmailNotification(ctx, &msg)
	default:
		w.logger.Warn("Unknown notification type", zap.String("type", msg.Type))
		return nil
	}
}

func (w *Worker) sendPushNotification(ctx context.Context, msg *NotificationMessage) error {
	// Get user's device tokens
	tokens, err := w.deviceRepo.GetUserDeviceTokens(ctx, msg.UserID)
	if err != nil {
		return fmt.Errorf("failed to get device tokens: %w", err)
	}

	if len(tokens) == 0 {
		w.logger.Debug("No device tokens for user", zap.String("user_id", msg.UserID.String()))
		return nil
	}

	for _, token := range tokens {
		if err := w.sender.SendPush(ctx, token, msg.Title, msg.Body, msg.Data); err != nil {
			w.logger.Warn("Failed to send push to device", zap.Error(err))
		}
	}

	return nil
}

func (w *Worker) sendSMSNotification(ctx context.Context, msg *NotificationMessage) error {
	phone := msg.Recipient
	if phone == "" {
		var err error
		phone, err = w.deviceRepo.GetUserPhone(ctx, msg.UserID)
		if err != nil || phone == "" {
			w.logger.Debug("No phone number for user", zap.String("user_id", msg.UserID.String()))
			return nil
		}
	}

	return w.sender.SendSMS(ctx, phone, msg.Body)
}

func (w *Worker) sendEmailNotification(ctx context.Context, msg *NotificationMessage) error {
	if w.emailSender == nil {
		return nil
	}

	email := msg.Recipient
	if email == "" {
		var err error
		email, err = w.deviceRepo.GetUserEmail(ctx, msg.UserID)
		if err != nil || email == "" {
			w.logger.Debug("No email for user", zap.String("user_id", msg.UserID.String()))
			return nil
		}
	}

	return w.emailSender.SendEmail(ctx, email, msg.Title, msg.Body)
}
