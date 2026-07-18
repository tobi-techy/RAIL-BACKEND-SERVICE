package platform

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
}

func (o *fakeOrchestrator) HandlePlatformMessage(_ context.Context, _, _, message, _ string, _ entities.Platform) (*PlatformReply, error) {
	o.lastMessage = message
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

func newTestProcessor(repo *fakeRepo, orch Orchestrator) (*Processor, *[]*OutboundMessage) {
	var sent []*OutboundMessage
	sendFunc := func(_ context.Context, m *OutboundMessage) error {
		sent = append(sent, m)
		return nil
	}
	p := NewProcessor(
		NewUserResolver(repo),
		orch,
		NewResponseBuilder(),
		NewLinkingService(repo, 900),
		nil, // voice transcoder not needed for these tests
		sendFunc,
	)
	return p, &sent
}

func TestProcessAction_ConfirmVoteExecutes(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, sent := newTestProcessor(repo, orch)

	pb := ActionPostback{
		Action:   "confirm",
		UserID:   "+15551234",
		SpaceID:  "space-1",
		Platform: "imessage",
	}
	raw, _ := json.Marshal(pb)

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
	p, _ := newTestProcessor(repo, orch)

	pb := ActionPostback{Action: "cancel", UserID: "+15551234", SpaceID: "space-1", Platform: "imessage"}
	raw, _ := json.Marshal(pb)

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
	p, sent := newTestProcessor(repo, orch)
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
	raw, _ := json.Marshal(msg)

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

func TestProcess_VoiceNoteWithoutTranscoderFallsBackToText(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, sent := newTestProcessor(repo, orch) // voice == nil

	msg := InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15551234",
		SpaceID:  "space-1",
		IsVoice:  true,
		AudioB64: base64.StdEncoding.EncodeToString([]byte("audio")),
	}
	raw, _ := json.Marshal(msg)

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
	p, _ := newTestProcessor(repo, orch)

	pb := ActionPostback{Action: "confirm", UserID: "+19999999", SpaceID: "space-1", Platform: "imessage"}
	raw, _ := json.Marshal(pb)

	if err := p.ProcessAction(context.Background(), raw); err != nil {
		t.Fatalf("ProcessAction: %v", err)
	}
	if orch.confirmCalls != 0 {
		t.Fatal("unlinked sender must not execute")
	}
}
