package di

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

// shopReaderAdapter reads a shop document — a trader's stock book, a
// distributor invoice, or a delivery note — through the Python reader on behalf
// of a linked owner.
//
// The owner's email and KYC-derived role are resolved here because the platform
// layer deliberately knows nothing about the users table: it hands over a rail
// user id, and this adapter turns that into the per-user token the reader
// expects. The reply text is passed through untouched.
type shopReaderAdapter struct {
	python   *ai.PythonAgentClient
	userRepo *repositories.UserRepository
	logger   *zap.Logger
}

// ReadShopDocument implements platform.ShopReader.
func (a *shopReaderAdapter) ReadShopDocument(ctx context.Context, req platform.ShopReadRequest) (*platform.ShopReadResult, error) {
	email, role := a.tokenClaims(ctx, req.UserID)
	answer, err := a.python.ReadShopDocument(ctx, req.UserID, email, role, ai.ShopDocumentRead{
		FileB64:   req.FileB64,
		MIME:      req.MIME,
		MessageID: req.MessageID,
		DocHint:   req.DocHint,
		Caption:   req.Caption,
	})
	if err != nil {
		return nil, err
	}
	return &platform.ShopReadResult{
		ReplyText:  answer.ReplyText,
		DocID:      answer.DocID,
		DocType:    answer.DocType,
		Flags:      answer.Flags,
		Confidence: answer.Confidence,
		ModelPath:  answer.ModelPath,
	}, nil
}

// tokenClaims loads the email + role used to mint the reader's JWT, mirroring
// the chat path. A failed lookup falls back to the read-only role, which fails
// closed: an unknown caller never gets the money-settling role.
func (a *shopReaderAdapter) tokenClaims(ctx context.Context, uid uuid.UUID) (email, role string) {
	role = "user"
	if a.userRepo == nil {
		a.logger.Warn("shop read: no user repository wired")
		return "", role
	}
	u, err := a.userRepo.GetByID(ctx, uid)
	if err != nil {
		a.logger.Warn("shop read: user lookup failed", zap.Error(err))
		return "", role
	}
	if u == nil {
		return "", role
	}
	return u.Email, pythonRole(u.KYCStatus, u.EmailVerified, u.IsActive)
}
