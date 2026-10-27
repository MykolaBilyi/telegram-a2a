package ports

import (
	"context"
	"telegram-a2a/internal/entities"
)

// Bot manages the Telegram polling loop and message routing.
type Bot interface {
	HealthChecker
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// SessionStore manages per-user A2A contextId sessions and pending HITL approvals.
type SessionStore interface {
	GetContextID(userID int64) (string, bool)
	SetContextID(userID int64, contextID string)
	DeleteContextID(userID int64)

	PutPendingApproval(callbackID string, approval entities.PendingApproval)
	PopPendingApproval(callbackID string) (entities.PendingApproval, bool)
}
