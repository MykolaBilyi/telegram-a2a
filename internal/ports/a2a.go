package ports

import (
	"context"
	"telegram-a2a/internal/entities"
)

// A2AClient communicates with an A2A JSON-RPC endpoint.
type A2AClient interface {
	HealthChecker
	SendMessage(ctx context.Context, messageText string, contextID string) (*entities.TaskResult, error)
}
