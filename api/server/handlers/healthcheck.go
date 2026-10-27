package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"telegram-a2a/internal/ports"
	"time"
)

type ComponentHealth struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type HealthResponse struct {
	Status     string                     `json:"status"`
	Components map[string]ComponentHealth `json:"components,omitempty"`
}

func NewHealthCheck(checkers ...map[string]ports.HealthChecker) http.Handler {
	merged := make(map[string]ports.HealthChecker)
	for _, m := range checkers {
		for k, v := range m {
			if v != nil {
				merged[k] = v
			}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		var (
			mu         sync.Mutex
			wg         sync.WaitGroup
			components = make(map[string]ComponentHealth, len(merged))
			allHealthy = true
		)

		for name, checker := range merged {
			wg.Add(1)
			go func(name string, checker ports.HealthChecker) {
				defer wg.Done()
				err := checker.HealthCheck(ctx)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					allHealthy = false
					components[name] = ComponentHealth{
						Status: "unhealthy",
						Error:  err.Error(),
					}
				} else {
					components[name] = ComponentHealth{
						Status: "ok",
					}
				}
			}(name, checker)
		}

		wg.Wait()

		resp := HealthResponse{
			Status:     "ok",
			Components: components,
		}
		statusCode := http.StatusOK
		if !allHealthy {
			resp.Status = "unhealthy"
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(resp)
	})
}
