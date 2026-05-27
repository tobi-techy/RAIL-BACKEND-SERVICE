package adapters

import (
	"context"
	"fmt"
	"html"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// EmailSenderAdapter adapts EmailService to the notification.EmailSenderService interface
type EmailSenderAdapter struct {
	emailService *EmailService
}

func NewEmailSenderAdapter(emailService *EmailService) *EmailSenderAdapter {
	return &EmailSenderAdapter{emailService: emailService}
}

func (a *EmailSenderAdapter) SendGenericEmail(ctx context.Context, to, subject, body string) error {
	safeSubject := html.EscapeString(subject)
	safeBody := html.EscapeString(body)

	innerHTML := renderHeader() +
		renderHeading(safeSubject) +
		renderBody(safeBody)

	htmlContent := renderBaseTemplate(innerHTML)

	return a.emailService.SendCustomEmail(ctx, to, subject, htmlContent, body)
}

// UserRepo is the minimal interface needed for email lookup.
type UserRepo interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// UserEmailLookup resolves a user's email via the user repository.
type UserEmailLookup struct {
	repo UserRepo
}

func NewUserEmailLookup(repo UserRepo) *UserEmailLookup {
	return &UserEmailLookup{repo: repo}
}

func (u *UserEmailLookup) GetEmailByUserID(ctx context.Context, userID uuid.UUID) (string, error) {
	user, err := u.repo.GetByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("lookup user email: %w", err)
	}
	return user.Email, nil
}
