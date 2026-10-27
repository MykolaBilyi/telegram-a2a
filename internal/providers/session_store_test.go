package providers_test

import (
	"fmt"
	"sync"
	"telegram-a2a/internal/entities"
	"telegram-a2a/internal/providers"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionStore_ContextIDLifecycle(t *testing.T) {
	store := providers.NewSessionStore(time.Minute)

	_, ok := store.GetContextID(42)
	assert.False(t, ok)

	store.SetContextID(42, "")
	_, ok = store.GetContextID(42)
	assert.False(t, ok)

	store.SetContextID(42, "ctx-abc-123")
	ctxID, ok := store.GetContextID(42)
	require.True(t, ok)
	assert.Equal(t, "ctx-abc-123", ctxID)

	store.DeleteContextID(42)
	_, ok = store.GetContextID(42)
	assert.False(t, ok)
}

func TestSessionStore_PendingApprovalPopOnce(t *testing.T) {
	store := providers.NewSessionStore(time.Minute)
	expected := entities.PendingApproval{
		ContextID: "ctx-1",
		UserID:    100,
		ChatID:    200,
	}

	store.PutPendingApproval("cb-1", expected)

	got, ok := store.PopPendingApproval("cb-1")
	require.True(t, ok)
	assert.Equal(t, expected, got)

	// Second pop must return false
	_, ok = store.PopPendingApproval("cb-1")
	assert.False(t, ok)

	// Unknown callback ID
	_, ok = store.PopPendingApproval("nonexistent")
	assert.False(t, ok)
}

func TestSessionStore_PendingApprovalExpiration(t *testing.T) {
	store := providers.NewSessionStore(10 * time.Millisecond)
	store.PutPendingApproval("cb-exp", entities.PendingApproval{
		ContextID: "ctx-exp",
		UserID:    1,
		ChatID:    1,
	})

	time.Sleep(25 * time.Millisecond)

	_, ok := store.PopPendingApproval("cb-exp")
	assert.False(t, ok, "expired approval should not be returned")
}

func TestSessionStore_DefaultTTLAndConcurrency(t *testing.T) {
	store := providers.NewSessionStore(0) // should default to 1h

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			userID := int64(id)
			cbID := fmt.Sprintf("cb-%d", id)
			ctxID := fmt.Sprintf("ctx-%d", id)

			store.SetContextID(userID, ctxID)
			gotCtx, ok := store.GetContextID(userID)
			assert.True(t, ok)
			assert.Equal(t, ctxID, gotCtx)

			store.PutPendingApproval(cbID, entities.PendingApproval{
				ContextID: ctxID,
				UserID:    userID,
				ChatID:    userID,
			})
			gotApproval, ok := store.PopPendingApproval(cbID)
			assert.True(t, ok)
			assert.Equal(t, ctxID, gotApproval.ContextID)

			store.DeleteContextID(userID)
		}(i)
	}
	wg.Wait()
}
