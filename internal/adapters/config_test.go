package adapters_test

import (
	"os"
	"path/filepath"
	"telegram-a2a/internal/adapters"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig_LoadsDefaultYAMLAndEnvOverrides(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-secret-token")
	t.Setenv("A2A_URL", "http://agent-service:8083/api/a2a/default/agent/")
	t.Setenv("TELEGRAM_ALLOWED_USERS", "111, 222, 333")

	cfg, err := adapters.NewConfig()
	require.NoError(t, err)

	assert.Equal(t, 10*time.Second, cfg.GetDuration("application.shutdownTimeout"))
	assert.Equal(t, ":8080", cfg.GetString("http.listenAddr"))

	tgSub := cfg.Sub("telegram")
	assert.Equal(t, "test-secret-token", tgSub.GetString("botToken"))
	assert.Equal(t, []int64{111, 222, 333}, tgSub.GetInt64Slice("allowedUsers"))

	a2aSub := cfg.Sub("a2a")
	assert.Equal(t, "http://agent-service:8083/api/a2a/default/agent/", a2aSub.GetString("url"))
	assert.Equal(t, 120*time.Second, a2aSub.GetDuration("timeout"))
}

func TestConfig_GetInt64Slice_VariousTypes(t *testing.T) {
	cfg := adapters.NewTestConfig(map[string]any{
		"ints":    []int{10, 20},
		"int64s":  []int64{100, 200},
		"anys":    []any{1, int64(2), float64(3), "4"},
		"csv":     "50, 60,invalid, 70",
		"empty":   "",
		"missing": nil,
	})

	assert.Equal(t, []int64{10, 20}, cfg.GetInt64Slice("ints"))
	assert.Equal(t, []int64{100, 200}, cfg.GetInt64Slice("int64s"))
	assert.Equal(t, []int64{1, 2, 3, 4}, cfg.GetInt64Slice("anys"))
	assert.Equal(t, []int64{50, 60, 70}, cfg.GetInt64Slice("csv"))
	assert.Empty(t, cfg.GetInt64Slice("empty"))
	assert.Nil(t, cfg.GetInt64Slice("missing"))
}

func TestNewConfig_WhenFileMissing_ReturnsError(t *testing.T) {
	emptyDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origDir) }()

	nested := filepath.Join(emptyDir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.Chdir(nested))

	_, err = adapters.NewConfig()
	assert.Error(t, err)
}

func TestConfig_Sub_WhenKeyDoesNotExist_ReturnsEmptyConfig(t *testing.T) {
	cfg := adapters.NewTestConfig()
	sub := cfg.Sub("nonexistent")
	assert.NotNil(t, sub)
	assert.Equal(t, "", sub.GetString("someKey"))
}
