package platform

import (
	"context"
	"html/template"
	"net/http"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// ConfirmHandler serves the public email confirmation page and validates
// one-time tokens for fund-moving actions.
type ConfirmHandler struct {
	tokens       *ConfirmTokenStore
	orchestrator interface {
		ConfirmAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, error)
		PeekPendingAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, bool)
	}
	logger *zap.Logger
}

func NewConfirmHandler(tokens *ConfirmTokenStore, orchestrator interface {
	ConfirmAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, error)
	PeekPendingAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, bool)
}, logger *zap.Logger) *ConfirmHandler {
	return &ConfirmHandler{tokens: tokens, orchestrator: orchestrator, logger: logger}
}

type confirmPageData struct {
	Success  bool
	Action   string
	Message  string
	AppURL   string
	RetryURL string
}

var confirmPageTemplate = template.Must(template.New("confirm").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Confirm transfer</title>
  <style>
    body { font-family: -apple-system, SF Pro Text, Helvetica Neue, sans-serif; background: #f5f5f5; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; }
    .card { background: #fff; border-radius: 16px; padding: 32px; max-width: 400px; width: 90%; text-align: center; box-shadow: 0 4px 12px rgba(0,0,0,0.08); }
    .icon { font-size: 48px; margin-bottom: 16px; }
    h1 { font-size: 20px; margin: 0 0 8px; color: #1a1a1a; }
    p { font-size: 15px; color: #666; margin: 0 0 24px; line-height: 1.5; }
    .btn { display: inline-block; padding: 14px 32px; background: #ff3e00; color: #fff; text-decoration: none; border-radius: 10px; font-weight: 600; font-size: 16px; }
    .btn-secondary { background: #e5e5e5; color: #333; margin-left: 8px; }
  </style>
</head>
<body>
  <div class="card">
    <div class="icon">{{if .Success}}✅{{else}}⚠️{{end}}</div>
    <h1>{{if .Success}}Transfer confirmed{{else}}Couldn't confirm{{end}}</h1>
    <p>{{.Message}}</p>
    {{if .Success}}
    <a href="{{.AppURL}}" class="btn">Open Rail</a>
    {{else}}
    <a href="{{.RetryURL}}" class="btn">Try again</a>
    {{end}}
  </div>
</body>
</html>
`))

func (h *ConfirmHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		h.render(w, confirmPageData{Success: false, Message: "Missing confirmation token."}, http.StatusBadRequest)
		return
	}

	payload, err := h.tokens.Validate(r.Context(), token)
	if err != nil {
		h.logger.Warn("invalid confirm token", zap.Error(err))
		h.render(w, confirmPageData{Success: false, Message: "This link is invalid or has expired."}, http.StatusBadRequest)
		return
	}

	action, ok := h.orchestrator.PeekPendingAction(r.Context(), payload.UserID, payload.ConvID)
	if !ok {
		h.render(w, confirmPageData{Success: false, Message: "This action has expired or was already handled."}, http.StatusGone)
		return
	}

	result, err := h.orchestrator.ConfirmAction(r.Context(), payload.UserID, payload.ConvID)
	if err != nil {
		h.logger.Error("confirm action failed", zap.Error(err), zap.String("action", action.Action))
		h.render(w, confirmPageData{Success: false, Message: "Something went wrong processing your transfer. Please try again from the app."}, http.StatusInternalServerError)
		return
	}

	_ = result
	h.render(w, confirmPageData{
		Success: true,
		Message: action.Description + " has been processed.",
		AppURL:  "rail://",
	}, http.StatusOK)
}

func (h *ConfirmHandler) render(w http.ResponseWriter, data confirmPageData, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = confirmPageTemplate.Execute(w, data)
}
