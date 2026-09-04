package channel

import (
	"context"
	"github.com/google/uuid"
)

// Platform represents a messaging platform.
type Platform string

const (
	PlatformIMessage Platform = "imessage"
	PlatformWhatsApp Platform = "whatsapp"
	PlatformTelegram Platform = "telegram"
	PlatformSMS      Platform = "sms"
	PlatformTerminal Platform = "terminal"
	PlatformUnknown  Platform = "unknown"
)

// NormalizePlatform normalizes a platform string to a known Platform.
func NormalizePlatform(s string) Platform {
	switch s {
	case "imessage", "IMessage", "iMessage":
		return PlatformIMessage
	case "whatsapp", "whatsapp business", "WhatsApp":
		return PlatformWhatsApp
	case "telegram", "Telegram":
		return PlatformTelegram
	case "sms", "SMS":
		return PlatformSMS
	case "terminal", "Terminal":
		return PlatformTerminal
	default:
		return PlatformUnknown
	}
}

// ChannelCapabilities defines what a platform can and cannot do.
type ChannelCapabilities struct {
	// SupportsPolls: native poll/chip support (iMessage, Telegram)
	SupportsPolls bool
	// SupportsEffects: iMessage message effects (celebration, confetti, etc.)
	SupportsEffects bool
	// SupportsQuickReplies: WhatsApp quick replies, Telegram inline keyboard buttons
	SupportsQuickReplies bool
	// SupportsInlineActions: Telegram inline keyboard, web app buttons
	SupportsInlineActions bool
	// SupportsRichCards: app cards, rich links, structured cards
	SupportsRichCards bool
	// SupportsThreading: reply threading / conversations
	SupportsThreading bool
	// SupportsVoiceIn: voice note inbound
	SupportsVoiceIn bool
	// SupportsImageIn: image inbound (receipt photos, etc.)
	SupportsImageIn bool
	// MaxBubblesPerReply: recommended max bubbles per turn to avoid wall of text
	MaxBubblesPerReply int
	// MaxCharsPerBubble: recommended max chars per bubble
	MaxCharsPerBubble int
	// PreferredTone: suggested tone for this platform
	PreferredTone string
}

// ChannelContext is the platform-agnostic context Miriam receives.
type ChannelContext struct {
	Platform       Platform
	PlatformUserID string
	ThreadID       string
	IdentityLinked bool
	Capabilities   ChannelCapabilities
	Locale         string
	PreferredTone  string
	MediaSupported bool
	UserID         uuid.UUID
}

// RenderStrategy tells the renderer how to present a tool result.
type RenderStrategy string

const (
	RenderText         RenderStrategy = "text"
	RenderCards        RenderStrategy = "cards"
	RenderPlan         RenderStrategy = "plan"
	RenderTrace        RenderStrategy = "trace"
	RenderPoll         RenderStrategy = "poll"
	RenderVoice        RenderStrategy = "voice"
	RenderQuickReplies RenderStrategy = "quick_replies"
)

// ChannelHints are hints from tools about how to render their output.
type ChannelHints struct {
	PreferredRender RenderStrategy `json:"preferred_render,omitempty"`
	MaxBubbles      int            `json:"max_bubbles,omitempty"`
	Collapsible     bool           `json:"collapsible,omitempty"`
	ActionChips     []ActionChip   `json:"action_chips,omitempty"`
}

// ActionChip is a tappable action for quick replies / inline keyboards.
type ActionChip struct {
	Label   string `json:"label"`
	Action  string `json:"action"`  // tool name to call
	Confirm bool   `json:"confirm"` // requires pending action confirmation
}

// PlanStep represents a single step in a multi-step plan.
type PlanStep struct {
	ID     int                    `json:"id"`
	Tool   string                 `json:"tool"`
	Status string                 `json:"status"` // "pending" | "running" | "done" | "failed"
	Check  string                 `json:"check,omitempty"`
	Params map[string]interface{} `json:"params,omitempty"`
	Error  string                 `json:"error,omitempty"`
	Result map[string]interface{} `json:"result,omitempty"`
}

// Plan represents a multi-step execution plan.
type Plan struct {
	ID          string     `json:"plan_id"`
	Steps       []PlanStep `json:"steps"`
	Checkpoints []string   `json:"checkpoints,omitempty"`
	Rollback    []string   `json:"rollback,omitempty"`
	Status      string     `json:"status"` // "draft" | "confirmed" | "running" | "completed" | "failed" | "cancelled"
	CreatedAt   int64      `json:"created_at"`
	UpdatedAt   int64      `json:"updated_at"`
}

// PlanHints is the channel hints specific to plan rendering.
type PlanHints struct {
	ChannelHints
	Plan *Plan `json:"plan,omitempty"`
}

// TraceHints is the channel hints for reasoning traces.
type TraceHints struct {
	ChannelHints
	TraceID string `json:"trace_id,omitempty"`
}

// CapabilityRegistry provides platform capability lookups.
type CapabilityRegistry struct {
	capabilities map[Platform]ChannelCapabilities
}

// NewCapabilityRegistry creates a new registry with default capabilities.
func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{
		capabilities: defaultCapabilities(),
	}
}

// Get returns the capabilities for a platform.
func (r *CapabilityRegistry) Get(platform Platform) ChannelCapabilities {
	if caps, ok := r.capabilities[platform]; ok {
		return caps
	}
	return defaultCapabilities()[PlatformUnknown]
}

// Set overrides capabilities for a platform (e.g., per-user customization).
func (r *CapabilityRegistry) Set(platform Platform, caps ChannelCapabilities) {
	r.capabilities[platform] = caps
}

// defaultCapabilities returns the built-in capability matrix.
func defaultCapabilities() map[Platform]ChannelCapabilities {
	return map[Platform]ChannelCapabilities{
		PlatformIMessage: {
			SupportsPolls:         true,
			SupportsEffects:       true,
			SupportsQuickReplies:  false,
			SupportsInlineActions: false,
			SupportsRichCards:     true,
			SupportsThreading:     true,
			SupportsVoiceIn:       true,
			SupportsImageIn:       true,
			MaxBubblesPerReply:    8,
			MaxCharsPerBubble:     2000,
			PreferredTone:         "warm, concise",
		},
		PlatformWhatsApp: {
			SupportsPolls:         false,
			SupportsEffects:       false,
			SupportsQuickReplies:  true,
			SupportsInlineActions: false,
			SupportsRichCards:     true,
			SupportsThreading:     false,
			SupportsVoiceIn:       true,
			SupportsImageIn:       true,
			MaxBubblesPerReply:    3,
			MaxCharsPerBubble:     4096,
			PreferredTone:         "warm, concise",
		},
		PlatformTelegram: {
			SupportsPolls:         true,
			SupportsEffects:       false,
			SupportsQuickReplies:  false,
			SupportsInlineActions: true,
			SupportsRichCards:     true,
			SupportsThreading:     true,
			SupportsVoiceIn:       true,
			SupportsImageIn:       true,
			MaxBubblesPerReply:    5,
			MaxCharsPerBubble:     4096,
			PreferredTone:         "concise, structured",
		},
		PlatformSMS: {
			SupportsPolls:         false,
			SupportsEffects:       false,
			SupportsQuickReplies:  false,
			SupportsInlineActions: false,
			SupportsRichCards:     false,
			SupportsThreading:     false,
			SupportsVoiceIn:       false,
			SupportsImageIn:       false,
			MaxBubblesPerReply:    1,
			MaxCharsPerBubble:     1600,
			PreferredTone:         "brief, action-oriented",
		},
		PlatformTerminal: {
			SupportsPolls:         false,
			SupportsEffects:       false,
			SupportsQuickReplies:  false,
			SupportsInlineActions: false,
			SupportsRichCards:     true,
			SupportsThreading:     false,
			SupportsVoiceIn:       false,
			SupportsImageIn:       false,
			MaxBubblesPerReply:    10,
			MaxCharsPerBubble:     4096,
			PreferredTone:         "technical, detailed",
		},
		PlatformUnknown: {
			SupportsPolls:         false,
			SupportsEffects:       false,
			SupportsQuickReplies:  false,
			SupportsInlineActions: false,
			SupportsRichCards:     false,
			SupportsThreading:     false,
			SupportsVoiceIn:       false,
			SupportsImageIn:       false,
			MaxBubblesPerReply:    1,
			MaxCharsPerBubble:     1000,
			PreferredTone:         "concise",
		},
	}
}

// BuildChannelContext creates a ChannelContext from platform info.
func BuildChannelContext(platform Platform, userID uuid.UUID, platformUserID, threadID string, identityLinked bool, locale string) ChannelContext {
	caps := defaultCapabilities()[platform]
	return ChannelContext{
		Platform:       platform,
		UserID:         userID,
		PlatformUserID: platformUserID,
		ThreadID:       threadID,
		IdentityLinked: identityLinked,
		Capabilities:   caps,
		Locale:         locale,
		PreferredTone:  caps.PreferredTone,
		MediaSupported: caps.SupportsImageIn || caps.SupportsVoiceIn,
	}
}

// BuildChannelContextFromRegistry creates a ChannelContext using a custom registry.
func BuildChannelContextFromRegistry(registry *CapabilityRegistry, platform Platform, userID uuid.UUID, platformUserID, threadID string, identityLinked bool, locale string) ChannelContext {
	caps := registry.Get(platform)
	return ChannelContext{
		Platform:       platform,
		UserID:         userID,
		PlatformUserID: platformUserID,
		ThreadID:       threadID,
		IdentityLinked: identityLinked,
		Capabilities:   caps,
		Locale:         locale,
		PreferredTone:  caps.PreferredTone,
		MediaSupported: caps.SupportsImageIn || caps.SupportsVoiceIn,
	}
}

// ToolResultHints are hints from tools about how to render their output on different platforms.
type ToolResultHints struct {
	PreferredRender RenderStrategy `json:"preferred_render,omitempty"`
	MaxBubbles      int            `json:"max_bubbles,omitempty"`
	Collapsible     bool           `json:"collapsible,omitempty"`
	ActionChips     []ActionChip   `json:"action_chips,omitempty"`
	PlanData        *PlanData      `json:"plan_data,omitempty"`
	TraceData       *TraceData     `json:"trace_data,omitempty"`
}

// NormalizePlatformCapabilities returns the capabilities for a normalized platform string.
// This is the convenience function used by the agent adapter to inject channel context.
func NormalizePlatformCapabilities(platform string) *ChannelCapabilities {
	p := NormalizePlatform(platform)
	reg := NewCapabilityRegistry()
	caps := reg.Get(p)
	return &caps
}

// PlanData carries multi-step plan information for rendering.
type PlanData struct {
	PlanID string `json:"plan_id"`
	Status string `json:"status"`
}

// TraceData carries reasoning trace information for rendering.
type TraceData struct {
	TraceID string `json:"trace_id"`
}

// PlatformContextReader is implemented by the bridge to provide platform context
// for a given HTTP request. The agent adapter calls this to determine which
// platform the user is messaging from so it can inject channel-specific context.
type PlatformContextReader interface {
	GetPlatformContext(ctx context.Context) (platform, platformUserID, threadID string, identityLinked bool, locale string)
}

// ContextBuilderDeps provides the dependencies needed to assemble agent context.
// This is injected by the agent adapter and used by the context builder.
type ContextBuilderDeps struct {
	PlatformReader PlatformContextReader
}
