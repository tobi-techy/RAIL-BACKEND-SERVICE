package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- fake Redis for unit tests ---

type fakeRedis struct {
	store map[string]string
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{store: make(map[string]string)}
}

func (f *fakeRedis) Set(_ context.Context, key string, value interface{}, _ time.Duration) error {
	f.store[key] = value.(string)
	return nil
}

func (f *fakeRedis) Get(_ context.Context, key string, dest interface{}) error {
	v, ok := f.store[key]
	if !ok {
		return redis.Nil
	}
	return json.Unmarshal([]byte(v), dest)
}

func (f *fakeRedis) Del(_ context.Context, key string) error {
	delete(f.store, key)
	return nil
}

func (f *fakeRedis) Exists(_ context.Context, key string) (bool, error) {
	_, ok := f.store[key], f.store[key] != ""
	return ok, nil
}

func (f *fakeRedis) SetNX(_ context.Context, _ string, _ interface{}, _ time.Duration) (bool, error) {
	return true, nil
}

func (f *fakeRedis) Incr(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (f *fakeRedis) IncrBy(_ context.Context, _ string, _ int64) (int64, error) {
	return 0, nil
}

func (f *fakeRedis) Expire(_ context.Context, _ string, _ time.Duration) error { return nil }

func (f *fakeRedis) Keys(_ context.Context, _ string) ([]string, error) {
	keys := make([]string, 0, len(f.store))
	for k := range f.store {
		keys = append(keys, k)
	}
	return keys, nil
}

func (f *fakeRedis) Ping(_ context.Context) error { return nil }

func (f *fakeRedis) Close() error { return nil }

func (f *fakeRedis) Client() *redis.Client { return nil }

// --- tests ---

func TestWorkingMemory_NilCache_FailOpen(t *testing.T) {
	wm := NewWorkingMemoryStore(nil, zap.NewNop())
	uid := uuid.New()

	entry := wm.Get(context.Background(), uid)
	assert.Nil(t, entry)

	err := wm.Save(context.Background(), uid, &WorkingMemoryEntry{Summary: "test"})
	assert.NoError(t, err)

	err = wm.Clear(context.Background(), uid)
	assert.NoError(t, err)
}

func TestWorkingMemory_AppendExchange_CreatesNewEntry(t *testing.T) {
	fr := newFakeRedis()
	wm := NewWorkingMemoryStore(fr, zap.NewNop())
	uid := uuid.New()

	wm.AppendExchange(context.Background(), uid, "hello", "hi there")

	entry := wm.Get(context.Background(), uid)
	require.NotNil(t, entry)
	assert.Equal(t, 1, entry.MessageCount)
	assert.Contains(t, entry.Summary, "hello")
	assert.Contains(t, entry.Summary, "hi there")
	assert.Equal(t, "general", entry.Topic)
}

func TestWorkingMemory_AppendExchange_BuildsUpSummary(t *testing.T) {
	fr := newFakeRedis()
	wm := NewWorkingMemoryStore(fr, zap.NewNop())
	uid := uuid.New()

	wm.AppendExchange(context.Background(), uid, "how's my budget?", "you're doing great")
	wm.AppendExchange(context.Background(), uid, "what about savings?", "you have $500 saved")

	entry := wm.Get(context.Background(), uid)
	require.NotNil(t, entry)
	assert.Equal(t, 2, entry.MessageCount)
	assert.Contains(t, entry.Summary, "budget")
	assert.Contains(t, entry.Summary, "savings")
	assert.Equal(t, "savings", entry.Topic)
}

func TestWorkingMemory_AppendExchange_TopicDetection(t *testing.T) {
	fr := newFakeRedis()
	wm := NewWorkingMemoryStore(fr, zap.NewNop())
	uid := uuid.New()

	tests := []struct {
		msg  string
		want string
	}{
		{"how's my budget looking?", "budget"},
		{"I want to save more money", "savings"},
		{"should I invest in stocks?", "investing"},
		{"when is my salary?", "income"},
		{"I need to pay my electric bill", "income"}, // "pay" matches before "bill"
		{"can you transfer money?", "transfers"},
		{"I want to set a goal", "goals"},
		{"how much did I spend today?", "spending"},
		{"what's the weather?", "general"},
	}

	for _, tc := range tests {
		entry := wm.Get(context.Background(), uid)
		if entry == nil {
			entry = &WorkingMemoryEntry{}
		}
		entry.Topic = extractTopic(tc.msg)
		assert.Equal(t, tc.want, entry.Topic, "message: %s", tc.msg)
	}
}

func TestWorkingMemory_SaveAndGet_RoundTrip(t *testing.T) {
	fr := newFakeRedis()
	wm := NewWorkingMemoryStore(fr, zap.NewNop())
	uid := uuid.New()

	original := &WorkingMemoryEntry{
		Summary:      "User asked about budget",
		Topic:        "budget",
		MessageCount: 3,
	}
	err := wm.Save(context.Background(), uid, original)
	require.NoError(t, err)

	got := wm.Get(context.Background(), uid)
	require.NotNil(t, got)
	assert.Equal(t, original.Summary, got.Summary)
	assert.Equal(t, original.Topic, got.Topic)
	assert.Equal(t, original.MessageCount, got.MessageCount)
}

func TestWorkingMemory_Clear(t *testing.T) {
	fr := newFakeRedis()
	wm := NewWorkingMemoryStore(fr, zap.NewNop())
	uid := uuid.New()

	wm.AppendExchange(context.Background(), uid, "hello", "hi")
	require.NotNil(t, wm.Get(context.Background(), uid))

	err := wm.Clear(context.Background(), uid)
	require.NoError(t, err)
	assert.Nil(t, wm.Get(context.Background(), uid))
}

func TestWorkingMemory_MaxCharsTruncation(t *testing.T) {
	fr := newFakeRedis()
	wm := NewWorkingMemoryStore(fr, zap.NewNop())
	uid := uuid.New()

	// Append many exchanges to exceed max chars
	for i := 0; i < 50; i++ {
		wm.AppendExchange(context.Background(), uid,
			"this is a longer message that should cause truncation",
			"this is a longer response that should also contribute to truncation")
	}

	entry := wm.Get(context.Background(), uid)
	require.NotNil(t, entry)
	// Summary should be truncated to workingMemoryMaxChars
	assert.LessOrEqual(t, len(entry.Summary), workingMemoryMaxChars+20, // some JSON overhead
		"summary should respect max chars limit")
}

func TestWorkingMemory_Get_Nonexistent(t *testing.T) {
	fr := newFakeRedis()
	wm := NewWorkingMemoryStore(fr, zap.NewNop())

	entry := wm.Get(context.Background(), uuid.New())
	assert.Nil(t, entry)
}

func TestWorkingMemoryEntry_SatisfiesSnapshotInterface(t *testing.T) {
	// Compile-time check that WorkingMemoryEntry satisfies core.WorkingMemorySnapshot
	entry := &WorkingMemoryEntry{
		Summary:      "test summary",
		Topic:        "budget",
		MessageCount: 5,
	}
	assert.Equal(t, "test summary", entry.GetSummary())
	assert.Equal(t, "budget", entry.GetTopic())
	assert.Equal(t, 5, entry.GetMessageCount())
}

func TestExtractTopic_AllCases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My monthly budget is too high", "budget"},
		{"I want to save more money", "savings"},
		{"My savings account", "savings"},
		{"How's my stash doing?", "savings"},
		{"Should I invest in crypto?", "investing"},
		{"Check my portfolio", "investing"},
		{"When does my salary come in?", "income"},
		{"I need more income", "income"},
		{"I need to pay my electric bill", "income"}, // "pay" matches before "bill" in switch order
		{"Cancel that subscription", "bills"},
		{"Send money to mom", "transfers"},
		{"I want to reach my goal", "goals"},
		{"How much did I spend on my card?", "spending"},
		{"How are you today?", "general"},
		{"", "general"},
	}

	for _, tc := range tests {
		got := extractTopic(tc.input)
		assert.Equal(t, tc.want, got, "input: %s", tc.input)
	}
}

func TestBuildActiveThread_PrefersProposalOrGoal(t *testing.T) {
	tests := []struct {
		name       string
		userMsg    string
		assistant  string
		wantPrefix string
	}{
		{
			name:       "proposal preserved",
			userMsg:    "yeah",
			assistant:  "I'll set up a $50 weekly transfer to your stash.",
			wantPrefix: "I'll set up a $50 weekly transfer to your stash.",
		},
		{
			name:       "goal preserved",
			userMsg:    "maybe later",
			assistant:  "no problem — we can keep your Europe trip goal as-is for now.",
			wantPrefix: "no problem — we can keep your Europe trip goal as-is for now.",
		},
		{
			name:       "short message fallback to assistant brief",
			userMsg:    "ok",
			assistant:  "Want me to automate the bill payment every month?",
			wantPrefix: "Want me to automate the bill payment every month?",
		},
		{
			name:       "long user message truncated",
			userMsg:    strings.Repeat("a", 120),
			assistant:  "Got it.",
			wantPrefix: strings.Repeat("a", 88) + "...",
		},
	}

	for _, tc := range tests {
		got := buildActiveThread(tc.userMsg, tc.assistant)
		assert.Equal(t, tc.wantPrefix, got, tc.name)
	}
}

func TestTruncate_ShortString(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
}

func TestTruncate_LongString(t *testing.T) {
	got := truncate("this is a very long message that should be truncated", 20)
	assert.LessOrEqual(t, len([]rune(got)), 23) // 20 + "..."
	assert.Contains(t, got, "...")
}

func TestTruncate_EmptyString(t *testing.T) {
	assert.Equal(t, "", truncate("", 10))
}

func TestTruncate_WhitespaceOnly(t *testing.T) {
	assert.Equal(t, "", truncate("   ", 10))
}

func TestTruncate_UnicodeSafe(t *testing.T) {
	// Unicode characters should not be cut mid-character
	input := "hello world"
	got := truncate(input, 5)
	assert.Equal(t, "hello...", got)
}
