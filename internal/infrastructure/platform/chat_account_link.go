package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

const chatLinkTTL = 15 * time.Minute

// chatLinkSession binds an OAuth round-trip to the iMessage sender who asked
// for it. The provider email they pick is the account key.
type chatLinkSession struct {
	Platform string `json:"platform"`
	SenderID string `json:"sender_id"`
	// Provider locks the nonce to the tapped button (apple/google) once the
	// start URL is opened. Empty means neither button opened yet.
	Provider string `json:"provider,omitempty"`
}

// ChatLinkOffer is the pair of taps we send in the thread.
type ChatLinkOffer struct {
	AppleURL  string
	GoogleURL string
}

// ChatAccountLinker starts Apple/Google linking for an unlinked chat sender
// and finishes it when the provider returns.
//
// The person chooses the email. A match links that existing Rail account.
// Any other email opens a new account and its wallet. There is no error for
// "wrong email".
type ChatAccountLinker struct {
	redis  cache.RedisClient
	social *socialauth.Service
	users  interface {
		GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error)
		CreateUserFromAuth(ctx context.Context, req *entities.RegisterRequest) (*entities.User, error)
		MarkEmailVerified(ctx context.Context, userID uuid.UUID) error
	}
	linking     *LinkingService
	provisioner OnboardingProvisioner
	publicBase  string
	logger      *zap.Logger
}

func NewChatAccountLinker(
	redis cache.RedisClient,
	social *socialauth.Service,
	users interface {
		GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error)
		CreateUserFromAuth(ctx context.Context, req *entities.RegisterRequest) (*entities.User, error)
		MarkEmailVerified(ctx context.Context, userID uuid.UUID) error
	},
	linking *LinkingService,
	provisioner OnboardingProvisioner,
	publicBase string,
	logger *zap.Logger,
) *ChatAccountLinker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ChatAccountLinker{
		redis:       redis,
		social:      social,
		users:       users,
		linking:     linking,
		provisioner: provisioner,
		publicBase:  strings.TrimRight(strings.TrimSpace(publicBase), "/"),
		logger:      logger,
	}
}

// PublicAPIOrigin strips a confirmation base such as
// https://api.userail.money/confirm down to the API origin that can host
// the OAuth callback.
func PublicAPIOrigin(confirmBase string) string {
	base := strings.TrimRight(strings.TrimSpace(confirmBase), "/")
	base = strings.TrimSuffix(base, "/confirm")
	return strings.TrimRight(base, "/")
}

func (l *ChatAccountLinker) Enabled() bool {
	return l != nil && l.redis != nil && l.social != nil && l.publicBase != ""
}

// Offer stores the sender and returns the two links they tap.
func (l *ChatAccountLinker) Offer(ctx context.Context, platform entities.Platform, senderID string) (ChatLinkOffer, error) {
	if !l.Enabled() || strings.TrimSpace(senderID) == "" {
		return ChatLinkOffer{}, fmt.Errorf("chat account linking is not configured")
	}
	nonce, err := randomNonce()
	if err != nil {
		return ChatLinkOffer{}, err
	}
	session := chatLinkSession{Platform: platform.String(), SenderID: senderID}
	if err := l.redis.Set(ctx, chatLinkKey(nonce), session, chatLinkTTL); err != nil {
		return ChatLinkOffer{}, err
	}
	return ChatLinkOffer{
		AppleURL:  l.publicBase + "/api/v1/chat-link/start/apple?nonce=" + nonce,
		GoogleURL: l.publicBase + "/api/v1/chat-link/start/google?nonce=" + nonce,
	}, nil
}

func (l *ChatAccountLinker) CallbackURL(provider ...entities.SocialProvider) string {
	if len(provider) == 1 && provider[0] != "" {
		return l.publicBase + "/api/v1/chat-link/callback/" + string(provider[0])
	}
	return l.publicBase + "/api/v1/chat-link/callback"
}

// ProviderAuthURL loads the sender session and returns the provider redirect.
// The nonce is single-use across providers: opening the Apple button binds it
// to Apple, so the Google button with the same nonce then expires (and vice
// versa). The provider travels in the OAuth state parameter and is enforced
// again in Complete.
func (l *ChatAccountLinker) ProviderAuthURL(ctx context.Context, provider entities.SocialProvider, nonce string) (string, error) {
	var session chatLinkSession
	if err := l.redis.Get(ctx, chatLinkKey(nonce), &session); err != nil || session.SenderID == "" {
		return "", fmt.Errorf("that link expired")
	}
	if session.Provider != "" && session.Provider != string(provider) {
		return "", fmt.Errorf("that link expired")
	}
	session.Provider = string(provider)
	if err := l.redis.Set(ctx, chatLinkKey(nonce), session, chatLinkTTL); err != nil {
		return "", err
	}
	// NOTE: redirect URI stays the base callback (registered with Apple and
	// Google). The provider travels in state and in the optional
	// /callback/:provider path — no OAuth client re-registration needed.
	return l.social.GetAuthURL(provider, l.CallbackURL(), linkState(provider, nonce))
}

// linkState encodes provider+nonce for the OAuth round-trip. Older links
// issued a bare nonce; parseLinkState accepts both.
func linkState(provider entities.SocialProvider, nonce string) string {
	return string(provider) + "|" + nonce
}

// parseLinkState splits "apple|NONCE" (new) or "NONCE" (legacy, provider
// resolved by the caller from the callback path).
func parseLinkState(state string) (provider entities.SocialProvider, nonce string) {
	if i := strings.Index(state, "|"); i > 0 {
		return entities.SocialProvider(state[:i]), state[i+1:]
	}
	return "", state
}

// Complete verifies the provider result, links the matching account, or
// creates one for a new email, then binds the iMessage sender.
// The nonce is consumed only on success so a failed attempt (expired code,
// user cancel) can retry within the TTL; a new Offer invalidates nothing
// already issued, but each nonce works exactly once.
func (l *ChatAccountLinker) Complete(ctx context.Context, provider entities.SocialProvider, code, idToken, state string) (created bool, err error) {
	stateProvider, nonce := parseLinkState(state)
	if stateProvider != "" {
		if stateProvider != provider {
			return false, fmt.Errorf("that link expired")
		}
	}
	// Legacy links carry a bare nonce and hit the un-suffixed callback with
	// provider unknown: resolve it from the session binding.
	var session chatLinkSession
	if err := l.redis.Get(ctx, chatLinkKey(nonce), &session); err != nil || session.SenderID == "" {
		return false, fmt.Errorf("that link expired")
	}
	if stateProvider == "" {
		if session.Provider == "" {
			return false, fmt.Errorf("that link expired")
		}
		provider = entities.SocialProvider(session.Provider)
	} else if session.Provider != "" && session.Provider != string(provider) {
		return false, fmt.Errorf("that link expired")
	}
	if provider != entities.SocialProviderApple && provider != entities.SocialProviderGoogle {
		return false, fmt.Errorf("unknown provider")
	}
	// Apple retries can post a code without an id_token; the Apple verifier
	// needs id_token, so fail with a restartable error instead of
	// misrouting the code to Google.
	if provider == entities.SocialProviderApple && strings.TrimSpace(idToken) == "" {
		return false, fmt.Errorf("Apple did not return an identity token; tap the Apple link once more for a fresh try")
	}

	info, err := l.social.Authenticate(ctx, &entities.SocialLoginRequest{
		Provider:    provider,
		Code:        code,
		IDToken:     idToken,
		RedirectURI: l.CallbackURL(),
		State:       state,
	})
	if err != nil {
		return false, err
	}
	email := strings.ToLower(strings.TrimSpace(info.Email))
	if email == "" {
		return false, fmt.Errorf("the provider did not share an email")
	}
	if !info.EmailVerified {
		return false, fmt.Errorf("that provider email is not verified; verify it with the provider and try again")
	}

	userID, created, err := l.resolveUser(ctx, provider, info, email)
	if err != nil {
		return false, err
	}
	plat := entities.Platform(session.Platform)
	identity, err := l.linking.LinkVerified(ctx, userID, plat, session.SenderID)
	if err != nil {
		return created, err
	}
	// LinkVerified is a no-op success when this user already linked this
	// platform. Confirm the sender actually got bound — otherwise the person
	// walks away thinking they are linked while the next message falls back
	// to guest onboarding.
	if identity.PlatformUserID != session.SenderID {
		return created, fmt.Errorf("this chat is already linked to a different account; unlink it first and try again")
	}
	_ = l.redis.Del(ctx, chatLinkKey(nonce))
	return created, nil
}

func (l *ChatAccountLinker) resolveUser(ctx context.Context, provider entities.SocialProvider, info *socialauth.SocialUserInfo, email string) (uuid.UUID, bool, error) {
	// An already-linked provider owns its account without re-proving the
	// email. Every other path requires a provider-verified address.
	if existingID, err := l.social.FindUserByProvider(ctx, provider, info.ProviderID); err == nil && existingID != uuid.Nil {
		return existingID, false, nil
	} else if err != nil {
		return uuid.Nil, false, fmt.Errorf("account lookup failed; try again")
	}
	if !info.EmailVerified {
		return uuid.Nil, false, fmt.Errorf("that provider email is not verified; verify it with the provider and try again")
	}
	if profile, err := l.users.GetByEmail(ctx, email); err == nil && profile != nil && profile.IsActive {
		_ = l.social.LinkAccount(ctx, profile.ID, info)
		return profile.ID, false, nil
	}
	user, err := l.users.CreateUserFromAuth(ctx, &entities.RegisterRequest{Email: email})
	if err != nil {
		if profile, gerr := l.users.GetByEmail(ctx, email); gerr == nil && profile != nil && profile.IsActive {
			_ = l.social.LinkAccount(ctx, profile.ID, info)
			return profile.ID, false, nil
		}
		return uuid.Nil, false, err
	}
	_ = l.users.MarkEmailVerified(ctx, user.ID)
	_ = l.social.LinkAccount(ctx, user.ID, info)
	if l.provisioner != nil {
		first := strings.TrimSpace(info.Name)
		if err := l.provisioner.ProvisionPhoneFirstUser(ctx, user.ID, first, "", ""); err != nil {
			l.logger.Warn("chat link wallet provisioning failed", zap.Error(err), zap.Stringer("user_id", user.ID))
		}
	}
	return user.ID, true, nil
}

func chatLinkKey(nonce string) string { return "chatlink:" + nonce }

func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// AccountLinkCopy is the message that accompanies the two links.
func AccountLinkCopy(offer ChatLinkOffer) string {
	var b strings.Builder
	b.WriteString("Link the Rail account you already use. On the next screen, pick that email.\n\n")
	if offer.AppleURL != "" {
		b.WriteString("Apple: " + offer.AppleURL + "\n")
	}
	if offer.GoogleURL != "" {
		b.WriteString("Google: " + offer.GoogleURL + "\n")
	}
	b.WriteString("\nIf you use a different email, I'll open a new account for it. Or reply with the email and I'll send a code.")
	return b.String()
}
