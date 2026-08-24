package platform

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// fakeRepo is an in-memory PlatformIdentityRepository for tests.
type fakeRepo struct {
	byID       map[uuid.UUID]*entities.PlatformIdentity
	byHash     map[string]uuid.UUID
	byPlatUser map[string]uuid.UUID // platform|platform_user_id -> id
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID:       map[uuid.UUID]*entities.PlatformIdentity{},
		byHash:     map[string]uuid.UUID{},
		byPlatUser: map[string]uuid.UUID{},
	}
}

func key(p entities.Platform, u string) string { return string(p) + "|" + u }

func (f *fakeRepo) GetByPlatformUser(_ context.Context, p entities.Platform, u string) (*entities.PlatformIdentity, error) {
	if id, ok := f.byPlatUser[key(p, u)]; ok {
		return f.byID[id], nil
	}
	return nil, errNotFound
}
func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*entities.PlatformIdentity, error) {
	if pi, ok := f.byID[id]; ok {
		return pi, nil
	}
	return nil, errNotFound
}
func (f *fakeRepo) GetByUserAndPlatform(_ context.Context, userID uuid.UUID, p entities.Platform) (*entities.PlatformIdentity, error) {
	for _, pi := range f.byID {
		if pi.UserID == userID && pi.Platform == p {
			return pi, nil
		}
	}
	return nil, errNotFound
}
func (f *fakeRepo) GetByHandshakeTokenHash(_ context.Context, hash string) (*entities.PlatformIdentity, error) {
	if id, ok := f.byHash[hash]; ok {
		return f.byID[id], nil
	}
	return nil, errNotFound
}
func (f *fakeRepo) ListByUser(_ context.Context, userID uuid.UUID) ([]*entities.PlatformIdentity, error) {
	var out []*entities.PlatformIdentity
	for _, pi := range f.byID {
		if pi.UserID == userID {
			out = append(out, pi)
		}
	}
	return out, nil
}
func (f *fakeRepo) Create(_ context.Context, pi *entities.PlatformIdentity) error {
	f.byID[pi.ID] = pi
	f.byPlatUser[key(pi.Platform, pi.PlatformUserID)] = pi.ID
	return nil
}
func (f *fakeRepo) SetHandshake(_ context.Context, id uuid.UUID, hash string, exp time.Time) error {
	pi := f.byID[id]
	pi.HandshakeTokenHash = &hash
	pi.HandshakeExpiresAt = &exp
	f.byHash[hash] = id
	return nil
}
func (f *fakeRepo) CompleteHandshake(_ context.Context, id uuid.UUID, platformUserID string) error {
	pi := f.byID[id]
	if pi.HandshakeTokenHash != nil {
		delete(f.byHash, *pi.HandshakeTokenHash)
	}
	delete(f.byPlatUser, key(pi.Platform, pi.PlatformUserID))
	pi.PlatformUserID = platformUserID
	now := time.Now()
	pi.LinkedAt = &now
	pi.HandshakeTokenHash = nil
	pi.HandshakeExpiresAt = nil
	f.byPlatUser[key(pi.Platform, platformUserID)] = id
	return nil
}
func (f *fakeRepo) TouchLastUsed(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

var errNotFound = &notFoundErr{}

type notFoundErr struct{}

func (*notFoundErr) Error() string { return "not found" }

// fakeOrchestrator records execution calls.
type fakeOrchestrator struct {
	confirmCalls int
	cancelCalls  int
	lastConvID   string
	lastMessage  string
	reply        *PlatformReply // when set, HandlePlatformMessage returns it
}

func (o *fakeOrchestrator) HandlePlatformMessage(_ context.Context, _, _, message, _ string, _ entities.Platform) (*PlatformReply, error) {
	o.lastMessage = message
	if o.reply != nil {
		return o.reply, nil
	}
	return &PlatformReply{Text: "your balance is $10"}, nil
}

// fakeVoice is a stub VoiceTranscoder.
type fakeVoice struct {
	transcript    string
	synthesized   bool
	transcribeErr error
}

func (f *fakeVoice) Available() bool { return true }
func (f *fakeVoice) Synthesize(context.Context, string) ([]byte, string, error) {
	f.synthesized = true
	return []byte("fake-audio"), "audio/mpeg", nil
}
func (f *fakeVoice) Transcribe(context.Context, []byte, string) (string, error) {
	return f.transcript, f.transcribeErr
}
func (o *fakeOrchestrator) ConfirmPlatformAction(_ context.Context, _, _, threadID string, _ entities.Platform) (*PlatformReply, error) {
	o.confirmCalls++
	o.lastConvID = threadID
	return &PlatformReply{Text: "done", Effect: EffectCelebration}, nil
}
func (o *fakeOrchestrator) HasPendingPlatformAction(_ context.Context, _, _, _ string, _ entities.Platform) bool {
	return false
}

func (o *fakeOrchestrator) CancelPlatformAction(_ context.Context, _, _, _ string, _ entities.Platform) (*PlatformReply, error) {
	o.cancelCalls++
	return &PlatformReply{Text: "cancelled"}, nil
}

func linkedIdentity(f *fakeRepo, platformUserID string) *entities.PlatformIdentity {
	now := time.Now()
	pi := &entities.PlatformIdentity{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		Platform:       entities.PlatformIMessage,
		PlatformUserID: platformUserID,
		LinkedAt:       &now,
	}
	f.byID[pi.ID] = pi
	f.byPlatUser[key(pi.Platform, pi.PlatformUserID)] = pi.ID
	return pi
}

func newTestProcessor(repo *fakeRepo, orch Orchestrator) (*Processor, *[]*OutboundMessage, *LinkingService) {
	var sent []*OutboundMessage
	sendFunc := func(_ context.Context, m *OutboundMessage) error {
		sent = append(sent, m)
		return nil
	}
	ls := NewLinkingService(repo, 900)
	p := NewProcessor(
		NewUserResolver(repo),
		orch,
		NewResponseBuilder(),
		ls,
		nil, // voice transcoder not needed for these tests
		sendFunc,
	)
	return p, &sent, ls
}

func TestProcessAction_ConfirmVoteExecutes(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, sent, _ := newTestProcessor(repo, orch)

	pb := ActionPostback{
		Action:   "confirm",
		UserID:   "+15551234",
		SpaceID:  "space-1",
		Platform: "imessage",
	}
	raw, err := json.Marshal(pb)
	if err != nil {
		t.Fatalf("marshal confirm postback: %v", err)
	}

	if err := p.ProcessAction(context.Background(), raw); err != nil {
		t.Fatalf("ProcessAction: %v", err)
	}
	if orch.confirmCalls != 1 {
		t.Fatalf("expected 1 confirm call, got %d", orch.confirmCalls)
	}
	if orch.lastConvID != "space-1" {
		t.Fatalf("thread not forwarded: %q", orch.lastConvID)
	}
	if len(*sent) == 0 {
		t.Fatal("expected a reply to be sent")
	}
}

func TestProcessAction_CancelVote(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, _, _ := newTestProcessor(repo, orch)

	pb := ActionPostback{Action: "cancel", UserID: "+15551234", SpaceID: "space-1", Platform: "imessage"}
	raw, err := json.Marshal(pb)
	if err != nil {
		t.Fatalf("marshal cancel postback: %v", err)
	}

	if err := p.ProcessAction(context.Background(), raw); err != nil {
		t.Fatalf("ProcessAction: %v", err)
	}
	if orch.cancelCalls != 1 || orch.confirmCalls != 0 {
		t.Fatalf("expected cancel path, got confirm=%d cancel=%d", orch.confirmCalls, orch.cancelCalls)
	}
}

func TestProcess_VoiceNoteTranscribesAndRepliesWithVoice(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, sent, _ := newTestProcessor(repo, orch)
	p.voice = &fakeVoice{transcript: "what's my balance"}

	msg := InboundMessage{
		Platform:  entities.PlatformIMessage,
		UserID:    "+15551234",
		ThreadID:  "space-1",
		SpaceID:   "space-1",
		MsgID:     "m1",
		IsVoice:   true,
		AudioB64:  base64.StdEncoding.EncodeToString([]byte("audio-bytes")),
		AudioMime: "audio/mp4",
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal voice message: %v", err)
	}

	if err := p.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if orch.lastMessage != "what's my balance" {
		t.Fatalf("orchestrator did not receive transcript, got %q", orch.lastMessage)
	}
	// Last sent message should be a voice note (typing indicator precedes it).
	if len(*sent) == 0 {
		t.Fatal("nothing sent")
	}
	last := (*sent)[len(*sent)-1]
	if last.ContentType != ContentTypeVoice || last.AudioB64 == "" {
		t.Fatalf("expected a voice reply, got content_type=%q audio_len=%d", last.ContentType, len(last.AudioB64))
	}
}

func TestProcessor_HandshakeTokenDuringOnboardingCompletesLink(t *testing.T) {
	repo := newFakeRepo()
	proc, sent, ls := newTestProcessor(repo, &fakeOrchestrator{})
	ob, _, _, _, _, _ := newTestOnboarder()
	proc.SetOnboarder(ob)

	// First message starts onboarding.
	raw, err := json.Marshal(InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15556666",
		Text:     "hi",
	})
	if err != nil {
		t.Fatalf("marshal first message: %v", err)
	}
	if err := proc.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !ob.HasSession(context.Background(), entities.PlatformIMessage, "+15556666") {
		t.Fatal("onboarding session should exist")
	}

	// Initiate a handshake from a different (existing) user.
	existingUserID := uuid.New()
	res, err := ls.InitiateHandshake(context.Background(), existingUserID, entities.PlatformIMessage)
	if err != nil {
		t.Fatalf("initiate handshake: %v", err)
	}

	// The same unlinked sender now texts the handshake token. It should
	// complete the link rather than be treated as an onboarding reply.
	raw, err = json.Marshal(InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15556666",
		Text:     res.Token,
	})
	if err != nil {
		t.Fatalf("marshal handshake message: %v", err)
	}
	if err := proc.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, "+15556666") {
		t.Fatal("onboarding session should be cleared after handshake")
	}
	if len(*sent) == 0 {
		t.Fatal("expected handshake confirmation reply")
	}
	last := (*sent)[len(*sent)-1]
	if !strings.Contains(last.Text, "linked") {
		t.Fatalf("expected link confirmation, got: %q", last.Text)
	}
}

func TestProcess_VoiceNoteWithoutTranscoderFallsBackToText(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, sent, _ := newTestProcessor(repo, orch) // voice == nil

	msg := InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15551234",
		SpaceID:  "space-1",
		IsVoice:  true,
		AudioB64: base64.StdEncoding.EncodeToString([]byte("audio")),
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal voice fallback message: %v", err)
	}

	if err := p.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if orch.lastMessage != "" {
		t.Fatal("orchestrator should not be called when voice can't be transcribed")
	}
	if len(*sent) != 1 || (*sent)[0].ContentType == ContentTypeVoice {
		t.Fatalf("expected a single text fallback message, got %d messages", len(*sent))
	}
}

func TestProcessAction_UnlinkedSenderIgnored(t *testing.T) {
	repo := newFakeRepo()
	orch := &fakeOrchestrator{}
	p, _, _ := newTestProcessor(repo, orch)

	pb := ActionPostback{Action: "confirm", UserID: "+19999999", SpaceID: "space-1", Platform: "imessage"}
	raw, err := json.Marshal(pb)
	if err != nil {
		t.Fatalf("marshal unlinked postback: %v", err)
	}

	if err := p.ProcessAction(context.Background(), raw); err != nil {
		t.Fatalf("ProcessAction: %v", err)
	}
	if orch.confirmCalls != 0 {
		t.Fatal("unlinked sender must not execute")
	}
}

func TestProcess_OrchestratorCardsRenderedInOutbound(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{
		reply: &PlatformReply{
			Text: "You've spent $123 this month.",
			Cards: []entities.InsightCard{
				{Type: "stat_grid", Title: "Spending Summary", Data: []entities.StatItem{
					{Label: "Total Spent", Value: "$123.45"},
					{Label: "Transactions", Value: "12"},
				}},
			},
		},
	}
	p, sent, _ := newTestProcessor(repo, orch)

	msg := InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15551234",
		ThreadID: "space-1",
		SpaceID:  "space-1",
		MsgID:    "m1",
		Text:     "what did i spend this month",
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal cards message: %v", err)
	}

	if err := p.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if len(*sent) == 0 {
		t.Fatal("nothing sent")
	}
	out := (*sent)[len(*sent)-1]
	if out.ContentType != ContentTypeCards {
		t.Fatalf("expected cards content type, got %q", out.ContentType)
	}
	if out.Text != "You've spent $123 this month." {
		t.Fatalf("cards reply lost its text: %q", out.Text)
	}
	if len(out.Cards) != 1 || out.Cards[0].Title != "Spending Summary" {
		t.Fatalf("cards not carried on outbound message: %+v", out.Cards)
	}
	// Wire contract: cards must survive JSON round-trip.
	rawOut, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal outbound: %v", err)
	}
	var decoded struct {
		ContentType string                 `json:"content_type"`
		Cards       []entities.InsightCard `json:"cards"`
	}
	if err := json.Unmarshal(rawOut, &decoded); err != nil {
		t.Fatalf("unmarshal outbound: %v", err)
	}
	if decoded.ContentType != string(ContentTypeCards) || len(decoded.Cards) != 1 {
		t.Fatalf("wire contract broken: %+v", decoded)
	}
}

func TestProcess_LinkedSenderContactCardNotForwarded(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, sent, _ := newTestProcessor(repo, orch)

	raw, err := json.Marshal(InboundMessage{
		Platform:  entities.PlatformIMessage,
		UserID:    "+15551234",
		ThreadID:  "space-1",
		IsContact: true,
		Contact:   &SharedContact{FirstName: "Ada", Phones: []string{"+2348012345678"}},
	})
	if err != nil {
		t.Fatalf("marshal contact: %v", err)
	}
	if err := p.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if orch.lastMessage != "" {
		t.Fatalf("orchestrator must not receive empty contact payload, got %q", orch.lastMessage)
	}
	if len(*sent) == 0 {
		t.Fatal("expected an ack to the linked sender")
	}
}
