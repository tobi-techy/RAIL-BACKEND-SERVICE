package platform

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

func (h *PlatformHandler) SetChatAccountLinker(linker *platform.ChatAccountLinker) {
	h.chatLinks = linker
}

func (h *PlatformHandler) ChatAccountLinker() *platform.ChatAccountLinker {
	if h == nil {
		return nil
	}
	return h.chatLinks
}

// StartChatLink redirects a tapped Apple or Google link to that provider.
// GET /api/v1/chat-link/start/:provider?nonce=
func (h *PlatformHandler) StartChatLink(c *gin.Context) {
	linker := h.ChatAccountLinker()
	if linker == nil || !linker.Enabled() {
		c.String(http.StatusServiceUnavailable, "Account linking is not available right now.")
		return
	}
	provider := entities.SocialProvider(strings.ToLower(c.Param("provider")))
	if provider != entities.SocialProviderApple && provider != entities.SocialProviderGoogle {
		c.String(http.StatusBadRequest, "Use Apple or Google.")
		return
	}
	authURL, err := linker.ProviderAuthURL(c.Request.Context(), provider, c.Query("nonce"))
	if err != nil {
		h.logger.Warn("chat link start failed", zap.Error(err))
		c.String(http.StatusBadRequest, "That link expired. Text Miriam and ask to link your account again.")
		return
	}
	c.Redirect(http.StatusFound, authURL)
}

// FinishChatLink accepts the provider callback. A matching email links the
// existing account. Any other email creates a new one.
// GET or POST /api/v1/chat-link/callback
func (h *PlatformHandler) FinishChatLink(c *gin.Context) {
	linker := h.ChatAccountLinker()
	if linker == nil || !linker.Enabled() {
		c.String(http.StatusServiceUnavailable, "Account linking is not available right now.")
		return
	}
	code := firstForm(c, "code")
	idToken := firstForm(c, "id_token")
	state := firstForm(c, "state")
	// Provider-specific callbacks (/callback/apple, /callback/google) carry
	// the provider in the path; the OAuth state carries provider|nonce for
	// new links. An explicit provider field wins over both. Legacy bare
	// callbacks fall back to the session binding inside Complete.
	provider := entities.SocialProvider(strings.ToLower(strings.TrimSpace(firstForm(c, "provider"))))
	if provider == "" {
		if p := strings.ToLower(strings.Trim(c.Param("provider"), "/")); p == "apple" || p == "google" {
			provider = entities.SocialProvider(p)
		}
	}
	if provider == "" {
		if i := strings.Index(state, "|"); i > 0 {
			if p := entities.SocialProvider(strings.ToLower(state[:i])); p == entities.SocialProviderApple || p == entities.SocialProviderGoogle {
				provider = p
			}
		}
	}
	if provider == "" {
		path := strings.ToLower(c.Request.URL.Path)
		if strings.Contains(path, "apple") {
			provider = entities.SocialProviderApple
		} else if strings.Contains(path, "google") {
			provider = entities.SocialProviderGoogle
		}
	}
	if provider == "" {
		// Last resort for legacy bare callbacks: payload shape. Apple
		// form_post includes id_token; Google redirects with code only.
		if idToken != "" {
			provider = entities.SocialProviderApple
		} else {
			provider = entities.SocialProviderGoogle
		}
	}
	created, err := linker.Complete(c.Request.Context(), provider, code, idToken, state)
	if err != nil {
		h.logger.Warn("chat link callback failed", zap.Error(err))
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(chatLinkPage(
			"That link didn't finish",
			"Text Miriam and ask to link your account again.",
		)))
		return
	}
	title := "You're linked"
	body := "That email is on an existing Rail account, and this chat is now on it. Go back to Messages."
	if created {
		title = "You're in"
		body = "That email didn't match an account, so I opened a new one and started its wallet. Go back to Messages."
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(chatLinkPage(title, body)))
}

func firstForm(c *gin.Context, key string) string {
	if v := strings.TrimSpace(c.PostForm(key)); v != "" {
		return v
	}
	return strings.TrimSpace(c.Query(key))
}

func chatLinkPage(title, body string) string {
	return "<!doctype html><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>" + title +
		"</title><body style=\"font:16px/1.4 -apple-system,sans-serif;padding:32px\"><h1>" + title +
		"</h1><p>" + body + "</p></body>"
}
