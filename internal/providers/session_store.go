package providers

import (
	"sync"
	"telegram-a2a/internal/entities"
	"telegram-a2a/internal/ports"
	"time"
)

type approvalEntry struct {
	approval  entities.PendingApproval
	expiresAt time.Time
}

// SessionStore is a thread-safe in-memory store for user contextIds and pending approvals.
type SessionStore struct {
	mu          sync.RWMutex
	contexts    map[int64]string
	approvals   map[string]approvalEntry
	approvalTTL time.Duration
	nowFunc     func() time.Time
}

var _ ports.SessionStore = (*SessionStore)(nil)

func NewSessionStore(approvalTTL time.Duration) *SessionStore {
	if approvalTTL <= 0 {
		approvalTTL = time.Hour
	}
	return &SessionStore{
		contexts:    make(map[int64]string),
		approvals:   make(map[string]approvalEntry),
		approvalTTL: approvalTTL,
		nowFunc:     time.Now,
	}
}

func (s *SessionStore) GetContextID(userID int64) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctxID, ok := s.contexts[userID]
	return ctxID, ok && ctxID != ""
}

func (s *SessionStore) SetContextID(userID int64, contextID string) {
	if contextID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contexts[userID] = contextID
}

func (s *SessionStore) DeleteContextID(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.contexts, userID)
}

func (s *SessionStore) PutPendingApproval(callbackID string, approval entities.PendingApproval) {
	now := s.nowFunc()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked(now)
	s.approvals[callbackID] = approvalEntry{
		approval:  approval,
		expiresAt: now.Add(s.approvalTTL),
	}
}

func (s *SessionStore) PopPendingApproval(callbackID string) (entities.PendingApproval, bool) {
	now := s.nowFunc()
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.approvals[callbackID]
	if !ok {
		s.evictExpiredLocked(now)
		return entities.PendingApproval{}, false
	}
	delete(s.approvals, callbackID)
	s.evictExpiredLocked(now)

	if now.After(entry.expiresAt) {
		return entities.PendingApproval{}, false
	}
	return entry.approval, true
}

func (s *SessionStore) evictExpiredLocked(now time.Time) {
	for id, entry := range s.approvals {
		if now.After(entry.expiresAt) {
			delete(s.approvals, id)
		}
	}
}
