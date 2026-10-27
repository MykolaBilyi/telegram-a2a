package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"telegram-a2a/internal/adapters"
	"telegram-a2a/internal/entities"
	"telegram-a2a/internal/providers"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubA2AClient struct {
	mu          sync.Mutex
	sendFunc    func(ctx context.Context, text, contextID string) (*entities.TaskResult, error)
	healthErr   error
	sentTexts   []string
	sentCtxIDs  []string
}

func (s *stubA2AClient) SendMessage(ctx context.Context, messageText string, contextID string) (*entities.TaskResult, error) {
	s.mu.Lock()
	s.sentTexts = append(s.sentTexts, messageText)
	s.sentCtxIDs = append(s.sentCtxIDs, contextID)
	fn := s.sendFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, messageText, contextID)
	}
	return &entities.TaskResult{
		ContextID: "default-ctx",
		Status:    entities.TaskStatus{State: entities.TaskStateCompleted},
	}, nil
}

func (s *stubA2AClient) HealthCheck(ctx context.Context) error {
	return s.healthErr
}

type recordedTGCall struct {
	Method  string
	Payload map[string]any
}

type fakeTelegramServer struct {
	mu      sync.Mutex
	updates []map[string]any
	calls   []recordedTGCall
	srv     *httptest.Server
}

func newFakeTelegramServer(t *testing.T, initialUpdates []map[string]any) *fakeTelegramServer {
	f := &fakeTelegramServer{
		updates: initialUpdates,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		method := parts[len(parts)-1]

		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)

		f.mu.Lock()
		if method != "getUpdates" {
			f.calls = append(f.calls, recordedTGCall{Method: method, Payload: payload})
		}
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":999,"username":"a2a_bot"}}`))
		case "getUpdates":
			f.mu.Lock()
			upds := f.updates
			f.updates = nil
			f.mu.Unlock()
			data, _ := json.Marshal(map[string]any{
				"ok":     true,
				"result": upds,
			})
			_, _ = w.Write(data)
		case "sendMessage":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":101,"chat":{"id":555}}}`))
		case "editMessageText":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":101,"chat":{"id":555}}}`))
		case "answerCallbackQuery":
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}
	}))
	t.Cleanup(func() { f.srv.Close() })
	return f
}

func (f *fakeTelegramServer) waitForCalls(t *testing.T, count int) []recordedTGCall {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.calls) >= count {
			res := make([]recordedTGCall, len(f.calls))
			copy(res, f.calls)
			f.mu.Unlock()
			return res
		}
		f.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("timed out waiting for %d telegram API calls, got %d: %+v", count, len(f.calls), f.calls)
	return nil
}

func TestNewTelegramBot_RequiresToken(t *testing.T) {
	cfg := adapters.NewTestConfig(map[string]any{"botToken": ""})
	bot, err := adapters.NewTelegramBot(cfg, &stubA2AClient{}, providers.NewSessionStore(time.Minute), adapters.NewNoopLogger())
	assert.Error(t, err)
	assert.Nil(t, bot)
}

func TestTelegramBot_CommandsAndAllowlist(t *testing.T) {
	updates := []map[string]any{
		{
			"update_id": 1,
			"message": map[string]any{
				"message_id": 10,
				"from":       map[string]any{"id": 999}, // unauthorized
				"chat":       map[string]any{"id": 555},
				"text":       "hello",
			},
		},
		{
			"update_id": 2,
			"message": map[string]any{
				"message_id": 11,
				"from":       map[string]any{"id": 42}, // authorized
				"chat":       map[string]any{"id": 555},
				"text":       "/start",
			},
		},
		{
			"update_id": 3,
			"message": map[string]any{
				"message_id": 12,
				"from":       map[string]any{"id": 42},
				"chat":       map[string]any{"id": 555},
				"text":       "/status",
			},
		},
		{
			"update_id": 4,
			"message": map[string]any{
				"message_id": 13,
				"from":       map[string]any{"id": 42},
				"chat":       map[string]any{"id": 555},
				"text":       "/new",
			},
		},
	}

	tgSrv := newFakeTelegramServer(t, updates)
	store := providers.NewSessionStore(time.Minute)
	store.SetContextID(42, "existing-ctx-id")

	cfg := adapters.NewTestConfig(map[string]any{
		"botToken":     "test-token",
		"apiURL":       tgSrv.srv.URL,
		"pollTimeout":  "10ms",
		"allowedUsers": []int64{42},
	})

	bot, err := adapters.NewTelegramBot(cfg, &stubA2AClient{}, store, adapters.NewNoopLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = bot.Start(ctx) }()

	calls := tgSrv.waitForCalls(t, 4)
	_ = bot.Stop(context.Background())

	assert.Contains(t, calls[0].Payload["text"], "Unauthorized")
	assert.Contains(t, calls[1].Payload["text"], "Welcome to the A2A Telegram Gateway")
	assert.Contains(t, calls[2].Payload["text"], "ONLINE")
	assert.Contains(t, calls[2].Payload["text"], "existing-ctx-id")
	assert.Contains(t, calls[3].Payload["text"], "Conversation session reset")

	_, hasCtx := store.GetContextID(42)
	assert.False(t, hasCtx, "/new should clear user's contextId")
}

func TestTelegramBot_NormalMessageAndSessionContinuity(t *testing.T) {
	updates := []map[string]any{
		{
			"update_id": 1,
			"message": map[string]any{
				"message_id": 10,
				"from":       map[string]any{"id": 42},
				"chat":       map[string]any{"id": 555},
				"text":       "list pods",
			},
		},
		{
			"update_id": 2,
			"message": map[string]any{
				"message_id": 11,
				"from":       map[string]any{"id": 42},
				"chat":       map[string]any{"id": 555},
				"text":       "scale nginx to 3",
			},
		},
	}

	tgSrv := newFakeTelegramServer(t, updates)
	store := providers.NewSessionStore(time.Minute)

	a2aStub := &stubA2AClient{
		sendFunc: func(ctx context.Context, text, contextID string) (*entities.TaskResult, error) {
			return &entities.TaskResult{
				ContextID: "ctx-session-777",
				Status:    entities.TaskStatus{State: entities.TaskStateCompleted},
				Artifacts: []entities.Artifact{
					{Parts: []entities.A2APart{{Kind: "text", Text: "Reply to: " + text}}},
				},
			}, nil
		},
	}

	cfg := adapters.NewTestConfig(map[string]any{
		"botToken":    "test-token",
		"apiURL":      tgSrv.srv.URL,
		"pollTimeout": "10ms",
	})
	bot, err := adapters.NewTelegramBot(cfg, a2aStub, store, adapters.NewNoopLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = bot.Start(ctx) }()

	// 2 messages -> each does sendMessage("Thinking...") + editMessageText("Reply to: ...") = 4 calls
	calls := tgSrv.waitForCalls(t, 4)
	_ = bot.Stop(context.Background())

	assert.Equal(t, "sendMessage", calls[0].Method)
	assert.Equal(t, "Thinking...", calls[0].Payload["text"])
	assert.Equal(t, "editMessageText", calls[1].Method)
	assert.Equal(t, "Reply to: list pods", calls[1].Payload["text"])

	assert.Equal(t, "sendMessage", calls[2].Method)
	assert.Equal(t, "editMessageText", calls[3].Method)
	assert.Equal(t, "Reply to: scale nginx to 3", calls[3].Payload["text"])

	a2aStub.mu.Lock()
	defer a2aStub.mu.Unlock()
	require.Len(t, a2aStub.sentCtxIDs, 2)
	assert.Equal(t, "", a2aStub.sentCtxIDs[0], "first message should have empty contextId")
	assert.Equal(t, "ctx-session-777", a2aStub.sentCtxIDs[1], "second message should reuse returned contextId")
}

func TestTelegramBot_HITLApprovalAndCallbackFlow(t *testing.T) {
	updates := []map[string]any{
		{
			"update_id": 1,
			"message": map[string]any{
				"message_id": 20,
				"from":       map[string]any{"id": 42},
				"chat":       map[string]any{"id": 555},
				"text":       "create staging namespace",
			},
		},
	}

	tgSrv := newFakeTelegramServer(t, updates)
	store := providers.NewSessionStore(time.Minute)

	callCount := 0
	a2aStub := &stubA2AClient{
		sendFunc: func(ctx context.Context, text, contextID string) (*entities.TaskResult, error) {
			callCount++
			if callCount == 1 {
				return &entities.TaskResult{
					ContextID: "ctx-hitl-999",
					Status: entities.TaskStatus{
						State: entities.TaskStateInputRequired,
						Message: &entities.A2AMessage{
							Parts: []entities.A2APart{
								{
									Kind: entities.PartKindData,
									Data: map[string]any{
										"name": "adk_request_confirmation",
										"args": map[string]any{
											"originalFunctionCall": map[string]any{
												"name": "k8s_create_resource",
												"args": map[string]any{
													"namespace": "staging",
													"kind":      "Namespace",
												},
											},
										},
									},
								},
							},
						},
					},
					History: []entities.A2AMessage{
						{
							Role:  "agent",
							Parts: []entities.A2APart{{Kind: "text", Text: "I need approval to create namespace staging."}},
						},
					},
				}, nil
			}
			return &entities.TaskResult{
				ContextID: "ctx-hitl-999",
				Status:    entities.TaskStatus{State: entities.TaskStateCompleted},
				Artifacts: []entities.Artifact{
					{Parts: []entities.A2APart{{Kind: "text", Text: "Namespace staging created successfully."}}},
				},
			}, nil
		},
	}

	cfg := adapters.NewTestConfig(map[string]any{
		"botToken":    "test-token",
		"apiURL":      tgSrv.srv.URL,
		"pollTimeout": "10ms",
	})
	bot, err := adapters.NewTelegramBot(cfg, a2aStub, store, adapters.NewNoopLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = bot.Start(ctx) }()

	// Wait for sendMessage("Thinking...") + editMessageText(with inline keyboard)
	calls := tgSrv.waitForCalls(t, 2)
	editCall := calls[1]
	assert.Equal(t, "editMessageText", editCall.Method)
	assert.Contains(t, editCall.Payload["text"], "k8s_create_resource")
	assert.Contains(t, editCall.Payload["text"], "namespace: staging")

	markup := editCall.Payload["reply_markup"].(map[string]any)
	kb := markup["inline_keyboard"].([]any)
	row := kb[0].([]any)
	approveBtn := row[0].(map[string]any)
	assert.Equal(t, "Approve", approveBtn["text"])
	callbackData := approveBtn["callback_data"].(string)
	assert.True(t, strings.HasPrefix(callbackData, "approve:"))

	// Simulate user tapping "Approve"
	tgSrv.mu.Lock()
	tgSrv.updates = append(tgSrv.updates, map[string]any{
		"update_id": 2,
		"callback_query": map[string]any{
			"id":   "cq-1",
			"from": map[string]any{"id": 42},
			"message": map[string]any{
				"message_id": 101,
				"chat":       map[string]any{"id": 555},
			},
			"data": callbackData,
		},
	})
	tgSrv.mu.Unlock()

	// Expect answerCallbackQuery + editMessageText("Approved. Processing...") + editMessageText("Namespace staging created successfully.")
	allCalls := tgSrv.waitForCalls(t, 5)
	_ = bot.Stop(context.Background())

	assert.Equal(t, "answerCallbackQuery", allCalls[2].Method)
	assert.Equal(t, "editMessageText", allCalls[3].Method)
	assert.Equal(t, "Approved. Processing...", allCalls[3].Payload["text"])
	assert.Equal(t, "editMessageText", allCalls[4].Method)
	assert.Equal(t, "Namespace staging created successfully.", allCalls[4].Payload["text"])

	a2aStub.mu.Lock()
	defer a2aStub.mu.Unlock()
	assert.Equal(t, []string{"create staging namespace", "approved"}, a2aStub.sentTexts)
	assert.Equal(t, []string{"", "ctx-hitl-999"}, a2aStub.sentCtxIDs)
}

func TestTelegramBot_HealthCheck(t *testing.T) {
	t.Run("healthy when getMe succeeds", func(t *testing.T) {
		tgSrv := newFakeTelegramServer(t, nil)
		cfg := adapters.NewTestConfig(map[string]any{
			"botToken": "test-token",
			"apiURL":   tgSrv.srv.URL,
		})
		bot, err := adapters.NewTelegramBot(cfg, &stubA2AClient{}, providers.NewSessionStore(time.Minute), adapters.NewNoopLogger())
		require.NoError(t, err)

		require.NoError(t, bot.HealthCheck(context.Background()))
	})

	t.Run("unhealthy when telegram API returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
		}))
		defer srv.Close()

		cfg := adapters.NewTestConfig(map[string]any{
			"botToken": "bad-token",
			"apiURL":   srv.URL,
		})
		bot, err := adapters.NewTelegramBot(cfg, &stubA2AClient{}, providers.NewSessionStore(time.Minute), adapters.NewNoopLogger())
		require.NoError(t, err)

		err = bot.HealthCheck(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Unauthorized")
	})
}

func TestChunkText(t *testing.T) {
	assert.Nil(t, adapters.ChunkText("", 4000))
	assert.Equal(t, []string{"short text"}, adapters.ChunkText("short text", 4000))

	longLine1 := strings.Repeat("a", 3000)
	longLine2 := strings.Repeat("b", 2000)
	combined := longLine1 + "\n" + longLine2

	chunks := adapters.ChunkText(combined, 4000)
	require.Len(t, chunks, 2)
	assert.Equal(t, longLine1+"\n", chunks[0])
	assert.Equal(t, longLine2, chunks[1])

	// Continuous string without newlines
	continuous := strings.Repeat("x", 8500)
	chunks2 := adapters.ChunkText(continuous, 4000)
	require.Len(t, chunks2, 3)
	assert.Len(t, chunks2[0], 4000)
	assert.Len(t, chunks2[1], 4000)
	assert.Len(t, chunks2[2], 500)
}

func TestTelegramBot_WhenA2AReturnsError_EditsThinkingMessageWithError(t *testing.T) {
	updates := []map[string]any{
		{
			"update_id": 1,
			"message": map[string]any{
				"message_id": 10,
				"from":       map[string]any{"id": 42},
				"chat":       map[string]any{"id": 555},
				"text":       "trigger error",
			},
		},
	}

	tgSrv := newFakeTelegramServer(t, updates)
	a2aStub := &stubA2AClient{
		sendFunc: func(ctx context.Context, text, contextID string) (*entities.TaskResult, error) {
			return nil, errors.New("connection refused")
		},
	}

	cfg := adapters.NewTestConfig(map[string]any{
		"botToken":    "test-token",
		"apiURL":      tgSrv.srv.URL,
		"pollTimeout": "10ms",
	})
	bot, err := adapters.NewTelegramBot(cfg, a2aStub, providers.NewSessionStore(time.Minute), adapters.NewNoopLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = bot.Start(ctx) }()

	calls := tgSrv.waitForCalls(t, 2)
	_ = bot.Stop(context.Background())

	assert.Equal(t, "editMessageText", calls[1].Method)
	assert.Contains(t, calls[1].Payload["text"], "Error contacting agent: connection refused")
}
