package ports

import "context"

// HealthChecker reports the health status of an external dependency or adapter.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}
