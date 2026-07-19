package ai

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoryAutopilotQueue implements AutopilotQueue in-memory for unit tests.
type memoryAutopilotQueue struct {
	mu      sync.Mutex
	actions map[uuid.UUID][]AutopilotQueueAction
}

func (m *memoryAutopilotQueue) Push(_ context.Context, userID uuid.UUID, action AutopilotQueueAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.actions == nil {
		m.actions = make(map[uuid.UUID][]AutopilotQueueAction)
	}
	m.actions[userID] = append(m.actions[userID], action)
	return nil
}

func (m *memoryAutopilotQueue) Pop(_ context.Context, userID uuid.UUID) (*AutopilotQueueAction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	actions := m.actions[userID]
	if len(actions) == 0 {
		return nil, nil
	}
	act := actions[0]
	m.actions[userID] = actions[1:]
	return &act, nil
}

func (m *memoryAutopilotQueue) List(_ context.Context, userID uuid.UUID) ([]AutopilotQueueAction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.actions[userID], nil
}

func (m *memoryAutopilotQueue) Clear(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.actions, userID)
	return nil
}

func (m *memoryAutopilotQueue) Len(_ context.Context, userID uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.actions[userID]), nil
}

func TestAutopilotQueue_PushAndList(t *testing.T) {
	q := &memoryAutopilotQueue{}
	ctx := context.Background()
	uid := uuid.New()

	actions, err := q.List(ctx, uid)
	require.NoError(t, err)
	assert.Empty(t, actions)

	err = q.Push(ctx, uid, AutopilotQueueAction{Tool: "alert_overspend", Reason: "test"})
	require.NoError(t, err)

	actions, err = q.List(ctx, uid)
	require.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.Equal(t, "alert_overspend", actions[0].Tool)
}

func TestAutopilotQueue_Pop(t *testing.T) {
	q := &memoryAutopilotQueue{}
	ctx := context.Background()
	uid := uuid.New()

	_ = q.Push(ctx, uid, AutopilotQueueAction{Tool: "a", Reason: "first"})
	_ = q.Push(ctx, uid, AutopilotQueueAction{Tool: "b", Reason: "second"})

	act, err := q.Pop(ctx, uid)
	require.NoError(t, err)
	require.NotNil(t, act)
	assert.Equal(t, "a", act.Tool)

	act, err = q.Pop(ctx, uid)
	require.NoError(t, err)
	require.NotNil(t, act)
	assert.Equal(t, "b", act.Tool)

	act, err = q.Pop(ctx, uid)
	require.NoError(t, err)
	assert.Nil(t, act)
}

func TestAutopilotQueue_Clear(t *testing.T) {
	q := &memoryAutopilotQueue{}
	ctx := context.Background()
	uid := uuid.New()

	_ = q.Push(ctx, uid, AutopilotQueueAction{Tool: "test", Reason: "x"})
	err := q.Clear(ctx, uid)
	require.NoError(t, err)

	actions, err := q.List(ctx, uid)
	require.NoError(t, err)
	assert.Empty(t, actions)
}

func TestAutopilotQueue_Len(t *testing.T) {
	q := &memoryAutopilotQueue{}
	ctx := context.Background()
	uid := uuid.New()

	n, err := q.Len(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	_ = q.Push(ctx, uid, AutopilotQueueAction{Tool: "a"})
	_ = q.Push(ctx, uid, AutopilotQueueAction{Tool: "b"})

	n, err = q.Len(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}
