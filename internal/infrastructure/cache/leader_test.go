package cache

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func testRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

func TestLeaderLock_OnlyOneWinner(t *testing.T) {
	rdb, _ := testRedis(t)
	ctx := context.Background()

	a := NewLeaderLock(rdb, time.Second)
	b := NewLeaderLock(rdb, time.Second)

	okA, err := a.TryAcquire(ctx)
	require.NoError(t, err)
	require.True(t, okA)

	okB, err := b.TryAcquire(ctx)
	require.NoError(t, err)
	require.False(t, okB)

	require.NoError(t, a.Renew(ctx))
	require.ErrorIs(t, b.Renew(ctx), ErrNotLeader)

	a.Release(ctx)
	okB, err = b.TryAcquire(ctx)
	require.NoError(t, err)
	require.True(t, okB)
}

func TestLeaderLock_ExpiresThenOtherWins(t *testing.T) {
	rdb, mr := testRedis(t)
	ctx := context.Background()

	a := NewLeaderLock(rdb, 50*time.Millisecond)
	b := NewLeaderLock(rdb, time.Second)

	ok, err := a.TryAcquire(ctx)
	require.NoError(t, err)
	require.True(t, ok)

	mr.FastForward(80 * time.Millisecond)

	ok, err = b.TryAcquire(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.ErrorIs(t, a.Renew(ctx), ErrNotLeader)
}
