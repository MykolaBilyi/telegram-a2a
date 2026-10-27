package adapters_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"telegram-a2a/internal/adapters"
	"telegram-a2a/internal/entities"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAgentCard = `{
	"name": "test-agent",
	"description": "A test agent",
	"version": "1.0.0",
	"supportedInterfaces": [
		{
			"url": "%s",
			"protocolBinding": "JSONRPC",
			"protocolVersion": "1.0"
		}
	]
}`

func newAgentCardServer(t *testing.T, messageEndpoint string) *httptest.Server {
	cardJSON := []byte(fmt.Sprintf(testAgentCard, messageEndpoint))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If request path ends with agent-card.json, return the card
		if strings.HasSuffix(r.URL.Path, "agent-card.json") || r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cardJSON)
			return
		}
		// Otherwise treat as message endpoint
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"contextId":"ctx-123","status":{"state":"completed"},"artifacts":[{"parts":[{"kind":"text","text":"ok"}]}]}}`))
	}))
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestNewA2AClient_WhenCardURLMissing_ReturnsError(t *testing.T) {
	cfg := adapters.NewTestConfig(map[string]any{"cardUrl": "   "})
	client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
	assert.Error(t, err)
	assert.Nil(t, client)
}

func TestNewA2AClient_WhenAgentCardUnavailable_FailsHard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := adapters.NewTestConfig(map[string]any{
		"cardUrl": srv.URL + "/.well-known/agent-card.json",
	})
	client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "404")
}

func TestA2AClient_SendMessage_UsesEndpointFromAgentCard(t *testing.T) {
	var receivedPath string
	var receivedReq entities.JSONRPCRequest

	msgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&receivedReq)
		w.Header().Set("Content-Type", "application/json")
		resp := entities.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      receivedReq.ID,
			Result: &entities.TaskResult{
				ContextID: "ctx-returned-123",
				Status:    entities.TaskStatus{State: entities.TaskStateCompleted},
				Artifacts: []entities.Artifact{
					{Parts: []entities.A2APart{{Kind: entities.PartKindText, Text: "Here are your pods"}}},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer msgSrv.Close()

	cardSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		card := map[string]any{
			"name":    "test-agent",
			"version": "1.0.0",
			"supportedInterfaces": []map[string]any{
				{"url": msgSrv.URL + "/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
			},
		}
		_ = json.NewEncoder(w).Encode(card)
	}))
	defer cardSrv.Close()

	cfg := adapters.NewTestConfig(map[string]any{
		"cardUrl": cardSrv.URL + "/.well-known/agent-card.json",
	})
	client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
	require.NoError(t, err)

	result, err := client.SendMessage(context.Background(), "list my pods", "ctx-existing-001")
	require.NoError(t, err)

	assert.Equal(t, "/rpc", receivedPath)
	assert.Equal(t, "ctx-existing-001", receivedReq.Params.Message.ContextID)
	assert.Equal(t, "ctx-returned-123", result.ContextID)
	assert.Equal(t, "Here are your pods", adapters.ExtractText(result))
}

func TestA2AClient_SendMessage_FirstMessageOmitsContextID(t *testing.T) {
	var rawBody map[string]any

	msgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&rawBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"contextId":"new-ctx","status":{"state":"completed"}}}`))
	}))
	defer msgSrv.Close()

	cardSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		card := map[string]any{
			"name":    "test-agent",
			"version": "1.0.0",
			"supportedInterfaces": []map[string]any{
				{"url": msgSrv.URL + "/", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
			},
		}
		_ = json.NewEncoder(w).Encode(card)
	}))
	defer cardSrv.Close()

	cfg := adapters.NewTestConfig(map[string]any{
		"cardUrl": cardSrv.URL + "/.well-known/agent-card.json",
	})
	client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
	require.NoError(t, err)

	res, err := client.SendMessage(context.Background(), "hello", "")
	require.NoError(t, err)
	assert.Equal(t, "new-ctx", res.ContextID)

	params := rawBody["params"].(map[string]any)
	message := params["message"].(map[string]any)
	_, hasContextID := message["contextId"]
	assert.False(t, hasContextID, "contextId should be omitted on first message")
}

func TestA2AClient_SendMessage_HandlesErrors(t *testing.T) {
	t.Run("non-2xx status code", func(t *testing.T) {
		msgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
		}))
		defer msgSrv.Close()

		cardSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			card := map[string]any{
				"name":    "test-agent",
				"version": "1.0.0",
				"supportedInterfaces": []map[string]any{
					{"url": msgSrv.URL + "/", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
				},
			}
			_ = json.NewEncoder(w).Encode(card)
		}))
		defer cardSrv.Close()

		cfg := adapters.NewTestConfig(map[string]any{
			"cardUrl": cardSrv.URL + "/.well-known/agent-card.json",
		})
		client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
		require.NoError(t, err)

		_, err = client.SendMessage(context.Background(), "hi", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "502")
	})

	t.Run("jsonrpc error response", func(t *testing.T) {
		msgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"Method not found"}}`))
		}))
		defer msgSrv.Close()

		cardSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			card := map[string]any{
				"name":    "test-agent",
				"version": "1.0.0",
				"supportedInterfaces": []map[string]any{
					{"url": msgSrv.URL + "/", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
				},
			}
			_ = json.NewEncoder(w).Encode(card)
		}))
		defer cardSrv.Close()

		cfg := adapters.NewTestConfig(map[string]any{
			"cardUrl": cardSrv.URL + "/.well-known/agent-card.json",
		})
		client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
		require.NoError(t, err)

		_, err = client.SendMessage(context.Background(), "hi", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Method not found")
	})
}

func TestExtractText_ThreeTierFallback(t *testing.T) {
	t.Run("nil result returns empty", func(t *testing.T) {
		assert.Equal(t, "", adapters.ExtractText(nil))
	})

	t.Run("tier 1: extracts from last artifact", func(t *testing.T) {
		res := &entities.TaskResult{
			Artifacts: []entities.Artifact{
				{Parts: []entities.A2APart{{Kind: "text", Text: "first"}}},
				{Parts: []entities.A2APart{{Kind: "text", Text: "line 1"}, {Kind: "text", Text: "line 2"}}},
			},
			History: []entities.A2AMessage{
				{Role: "agent", Parts: []entities.A2APart{{Kind: "text", Text: "history text"}}},
			},
		}
		assert.Equal(t, "line 1\nline 2", adapters.ExtractText(res))
	})

	t.Run("tier 2: extracts from last agent message in history when no artifacts", func(t *testing.T) {
		res := &entities.TaskResult{
			Status: entities.TaskStatus{State: entities.TaskStateInputRequired},
			History: []entities.A2AMessage{
				{Role: "user", Parts: []entities.A2APart{{Kind: "text", Text: "create staging ns"}}},
				{Role: "agent", Parts: []entities.A2APart{{Kind: "text", Text: "I will create the staging namespace."}}},
				{Role: "user", Parts: []entities.A2APart{{Kind: "text", Text: "another user msg"}}},
			},
		}
		assert.Equal(t, "I will create the staging namespace.", adapters.ExtractText(res))
	})

	t.Run("tier 3: extracts from status.message.parts when no artifacts or agent history", func(t *testing.T) {
		res := &entities.TaskResult{
			Status: entities.TaskStatus{
				State: entities.TaskStateInputRequired,
				Message: &entities.A2AMessage{
					Parts: []entities.A2APart{
						{Kind: "text", Text: "Status fallback message"},
					},
				},
			},
		}
		assert.Equal(t, "Status fallback message", adapters.ExtractText(res))
	})
}

func TestExtractADKConfirmation(t *testing.T) {
	t.Run("parses approval confirmation from status.message", func(t *testing.T) {
		res := &entities.TaskResult{
			ContextID: "abc-123",
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
									"toolConfirmation": map[string]any{
										"hint": "Confirm creation of namespace staging",
									},
								},
							},
						},
					},
				},
			},
		}

		conf := adapters.ExtractADKConfirmation(res)
		require.NotNil(t, conf)
		assert.Equal(t, entities.ConfirmationTypeApproval, conf.Type)
		assert.Equal(t, "k8s_create_resource", conf.ToolName)
		assert.Equal(t, "staging", conf.ToolArgs["namespace"])
		assert.Equal(t, "Namespace", conf.ToolArgs["kind"])
		assert.Equal(t, "Confirm creation of namespace staging", conf.Hint)
	})

	t.Run("parses ask_user confirmation with choices", func(t *testing.T) {
		data := map[string]any{
			"name": "adk_request_confirmation",
			"args": map[string]any{
				"originalFunctionCall": map[string]any{
					"name": "ask_user",
					"args": map[string]any{
						"questions": []any{
							map[string]any{
								"question": "Which namespace?",
								"choices":  []any{"default", "staging", "prod"},
							},
						},
					},
				},
			},
		}

		conf := adapters.ParseADKConfirmation(data)
		require.NotNil(t, conf)
		assert.Equal(t, entities.ConfirmationTypeAskUser, conf.Type)
		require.Len(t, conf.Questions, 1)
		assert.Equal(t, "Which namespace?", conf.Questions[0].Question)
		assert.Equal(t, []string{"default", "staging", "prod"}, conf.Questions[0].Choices)
	})

	t.Run("parses ask_user when questions is a plain string", func(t *testing.T) {
		data := map[string]any{
			"name": "adk_request_confirmation",
			"args": map[string]any{
				"originalFunctionCall": map[string]any{
					"name": "ask_user",
					"args": map[string]any{
						"questions": "What replica count do you want?",
					},
				},
			},
		}

		conf := adapters.ParseADKConfirmation(data)
		require.NotNil(t, conf)
		assert.Equal(t, entities.ConfirmationTypeAskUser, conf.Type)
		require.Len(t, conf.Questions, 1)
		assert.Equal(t, "What replica count do you want?", conf.Questions[0].Question)
	})

	t.Run("returns generic question fallback when input-required has no adk data", func(t *testing.T) {
		res := &entities.TaskResult{
			Status: entities.TaskStatus{
				State: entities.TaskStateInputRequired,
			},
		}
		conf := adapters.ExtractADKConfirmation(res)
		require.NotNil(t, conf)
		assert.Equal(t, entities.ConfirmationTypeQuestion, conf.Type)
	})
}

func TestA2AClient_HealthCheck(t *testing.T) {
	t.Run("healthy when agent card and endpoint are valid", func(t *testing.T) {
		msgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer msgSrv.Close()

		cardSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			card := map[string]any{
				"name":    "test-agent",
				"version": "1.0.0",
				"supportedInterfaces": []map[string]any{
					{"url": msgSrv.URL + "/", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
				},
			}
			_ = json.NewEncoder(w).Encode(card)
		}))
		defer cardSrv.Close()

		cfg := adapters.NewTestConfig(map[string]any{
			"cardUrl": cardSrv.URL + "/.well-known/agent-card.json",
		})
		client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
		require.NoError(t, err)

		err = client.HealthCheck(context.Background())
		require.NoError(t, err)
	})

	t.Run("unhealthy when agent card is invalid", func(t *testing.T) {
		cardSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not-json`))
		}))
		defer cardSrv.Close()

		cfg := adapters.NewTestConfig(map[string]any{
			"cardUrl": cardSrv.URL + "/.well-known/agent-card.json",
		})
		// NewA2AClient fails hard on startup if card is invalid
		_, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
		require.Error(t, err)
	})

	t.Run("unhealthy when endpoint returns 500", func(t *testing.T) {
		msgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer msgSrv.Close()

		cardSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			card := map[string]any{
				"name":    "test-agent",
				"version": "1.0.0",
				"supportedInterfaces": []map[string]any{
					{"url": msgSrv.URL + "/", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
				},
			}
			_ = json.NewEncoder(w).Encode(card)
		}))
		defer cardSrv.Close()

		cfg := adapters.NewTestConfig(map[string]any{
			"cardUrl": cardSrv.URL + "/.well-known/agent-card.json",
		})
		client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
		require.NoError(t, err)

		err = client.HealthCheck(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "500")
	})

	t.Run("unhealthy when endpoint is unreachable", func(t *testing.T) {
		msgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		msgSrv.Close()

		cardSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			card := map[string]any{
				"name":    "test-agent",
				"version": "1.0.0",
				"supportedInterfaces": []map[string]any{
					{"url": msgSrv.URL + "/", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
				},
			}
			_ = json.NewEncoder(w).Encode(card)
		}))
		defer cardSrv.Close()

		cfg := adapters.NewTestConfig(map[string]any{
			"cardUrl": cardSrv.URL + "/.well-known/agent-card.json",
		})
		client, err := adapters.NewA2AClient(cfg, adapters.NewNoopLogger())
		require.NoError(t, err)

		err = client.HealthCheck(context.Background())
		require.Error(t, err)
	})
}

