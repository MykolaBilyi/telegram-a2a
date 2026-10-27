package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"telegram-a2a/api/server/handlers"
	"telegram-a2a/internal/ports"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockHealthChecker struct {
	err error
}

func (m *mockHealthChecker) HealthCheck(ctx context.Context) error {
	return m.err
}

func TestHealthCheck_WhenAllComponentsHealthy_Returns200OK(t *testing.T) {
	handler := handlers.NewHealthCheck(map[string]ports.HealthChecker{
		"telegram": &mockHealthChecker{err: nil},
		"a2a":      &mockHealthChecker{err: nil},
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp handlers.HealthResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "ok", resp.Components["telegram"].Status)
	assert.Equal(t, "ok", resp.Components["a2a"].Status)
}

func TestHealthCheck_WhenTelegramUnhealthy_Returns503(t *testing.T) {
	handler := handlers.NewHealthCheck(map[string]ports.HealthChecker{
		"telegram": &mockHealthChecker{err: errors.New("telegram getMe failed: 401 Unauthorized")},
		"a2a":      &mockHealthChecker{err: nil},
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var resp handlers.HealthResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "unhealthy", resp.Status)
	assert.Equal(t, "unhealthy", resp.Components["telegram"].Status)
	assert.Contains(t, resp.Components["telegram"].Error, "401 Unauthorized")
	assert.Equal(t, "ok", resp.Components["a2a"].Status)
}

func TestHealthCheck_WhenA2AUnhealthy_Returns503(t *testing.T) {
	handler := handlers.NewHealthCheck(map[string]ports.HealthChecker{
		"telegram": &mockHealthChecker{err: nil},
		"a2a":      &mockHealthChecker{err: errors.New("a2a endpoint unreachable")},
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var resp handlers.HealthResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "unhealthy", resp.Status)
	assert.Equal(t, "ok", resp.Components["telegram"].Status)
	assert.Equal(t, "unhealthy", resp.Components["a2a"].Status)
	assert.Contains(t, resp.Components["a2a"].Error, "unreachable")
}

func TestHealthCheck_WhenNoCheckersRegistered_Returns200OK(t *testing.T) {
	handler := handlers.NewHealthCheck()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
