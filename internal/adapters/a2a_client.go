package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"telegram-a2a/internal/entities"
	"telegram-a2a/internal/ports"
	"time"

	"github.com/google/uuid"
)

type A2AClient struct {
	cardURL      string
	cardCacheTTL time.Duration
	httpClient   *http.Client
	logger       ports.Logger

	mu             sync.RWMutex
	cachedCard     *entities.AgentCard
	cardFetchedAt  time.Time
	messageEndpoint string
}

var _ ports.A2AClient = (*A2AClient)(nil)

func NewA2AClient(cfg ports.Config, logger ports.Logger) (*A2AClient, error) {
	cfg.SetDefault("cardCacheTTL", 0*time.Second)

	cardURL := strings.TrimSpace(cfg.GetString("cardUrl"))
	if cardURL == "" {
		return nil, errors.New("agent cardUrl is required")
	}

	client := &A2AClient{
		cardURL:      cardURL,
		cardCacheTTL: cfg.GetDuration("cardCacheTTL"),
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		logger: logger,
	}

	// Fetch agent card on startup (fail hard if unavailable)
	if err := client.refreshAgentCard(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to fetch agent card: %w", err)
	}

	return client, nil
}

func (c *A2AClient) refreshAgentCard(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cardURL, nil)
	if err != nil {
		return fmt.Errorf("create agent card request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent card endpoint unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read agent card response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent card returned unexpected status: %d", resp.StatusCode)
	}

	var card entities.AgentCard
	if err := json.Unmarshal(respBytes, &card); err != nil {
		return fmt.Errorf("invalid agent card json: %w", err)
	}

	if strings.TrimSpace(card.Name) == "" {
		return errors.New("invalid agent card: missing name")
	}

	if len(card.SupportedInterfaces) == 0 {
		return errors.New("invalid agent card: no supported interfaces")
	}

	endpoint := strings.TrimSpace(card.SupportedInterfaces[0].URL)
	if endpoint == "" {
		return errors.New("invalid agent card: first supported interface has no url")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cachedCard = &card
	c.cardFetchedAt = time.Now()
	c.messageEndpoint = endpoint

	c.logger.Info("agent card fetched", ports.Fields{
		"name":     card.Name,
		"version":  card.Version,
		"endpoint": endpoint,
	})

	return nil
}

func (c *A2AClient) getAgentCard(ctx context.Context) (*entities.AgentCard, error) {
	c.mu.RLock()
	card := c.cachedCard
	fetchedAt := c.cardFetchedAt
	c.mu.RUnlock()

	// 0 = unlimited cache (never refresh after startup)
	if c.cardCacheTTL > 0 && time.Since(fetchedAt) > c.cardCacheTTL {
		if err := c.refreshAgentCard(ctx); err != nil {
			return nil, err
		}
		c.mu.RLock()
		card = c.cachedCard
		c.mu.RUnlock()
	}

	if card == nil {
		return nil, errors.New("agent card not cached")
	}
	return card, nil
}

func (c *A2AClient) getMessageEndpoint(ctx context.Context) (string, error) {
	c.mu.RLock()
	endpoint := c.messageEndpoint
	c.mu.RUnlock()

	if endpoint != "" {
		return endpoint, nil
	}

	// Refresh if endpoint not yet set
	if err := c.refreshAgentCard(ctx); err != nil {
		return "", err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.messageEndpoint == "" {
		return "", errors.New("agent card has no message endpoint")
	}
	return c.messageEndpoint, nil
}

func (c *A2AClient) SendMessage(ctx context.Context, messageText string, contextID string) (*entities.TaskResult, error) {
	endpoint, err := c.getMessageEndpoint(ctx)
	if err != nil {
		return nil, err
	}

	msgID := uuid.NewString()
	msg := entities.A2AMessage{
		Role:      "user",
		Kind:      "message",
		MessageID: msgID,
		ContextID: contextID,
		Parts: []entities.A2APart{
			{
				Kind: entities.PartKindText,
				Text: messageText,
			},
		},
	}

	reqPayload := entities.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      msgID,
		Method:  entities.A2AMethodMessageSend,
		Params: entities.MessageParams{
			Message: msg,
		},
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal a2a request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create a2a request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send a2a request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read a2a response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected a2a status code %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	var rpcResp entities.JSONRPCResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal a2a response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("a2a rpc error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if rpcResp.Result == nil {
		return &entities.TaskResult{}, nil
	}

	return rpcResp.Result, nil
}

// HealthCheck validates the agent card and its message endpoint.
func (c *A2AClient) HealthCheck(ctx context.Context) error {
	card, err := c.getAgentCard(ctx)
	if err != nil {
		return err
	}

	if len(card.SupportedInterfaces) == 0 {
		return errors.New("agent card has no supported interfaces")
	}

	endpoint := strings.TrimSpace(card.SupportedInterfaces[0].URL)
	if endpoint == "" {
		return errors.New("agent card first interface has no url")
	}

	// Verify the message endpoint is reachable
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create health check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("message endpoint unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Accept any non-5xx (endpoint may not support HEAD, that's fine)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("message endpoint returned server error: %d", resp.StatusCode)
	}

	return nil
}

// ExtractText extracts text from an A2A TaskResult using the 3-tier fallback chain:
// 1. Last artifact's text parts (completed responses)
// 2. Last agent message in history (input-required / HITL explanations)
// 3. Status message text parts
func ExtractText(result *entities.TaskResult) string {
	if result == nil {
		return ""
	}

	// 1. Check artifacts first (last artifact)
	if len(result.Artifacts) > 0 {
		lastArtifact := result.Artifacts[len(result.Artifacts)-1]
		if texts := collectTextParts(lastArtifact.Parts); len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}

	// 2. Check history in reverse — last agent message with text parts
	for i := len(result.History) - 1; i >= 0; i-- {
		msg := result.History[i]
		if msg.Role != "agent" {
			continue
		}
		if texts := collectTextParts(msg.Parts); len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}

	// 3. Check status message
	if result.Status.Message != nil {
		for _, p := range result.Status.Message.Parts {
			if p.Kind == entities.PartKindText && p.Text != "" {
				return p.Text
			}
		}
	}

	return ""
}

func collectTextParts(parts []entities.A2APart) []string {
	var texts []string
	for _, p := range parts {
		if p.Kind == entities.PartKindText && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return texts
}

// ExtractADKConfirmation inspects an input-required TaskResult for an adk_request_confirmation data part.
func ExtractADKConfirmation(result *entities.TaskResult) *entities.ADKConfirmation {
	if result == nil {
		return nil
	}

	if result.Status.Message != nil {
		for _, p := range result.Status.Message.Parts {
			if p.Kind == entities.PartKindData && p.Data != nil {
				if conf := ParseADKConfirmation(p.Data); conf != nil {
					return conf
				}
			}
		}
	}

	for i := len(result.History) - 1; i >= 0; i-- {
		msg := result.History[i]
		for _, p := range msg.Parts {
			if p.Kind == entities.PartKindData && p.Data != nil {
				if conf := ParseADKConfirmation(p.Data); conf != nil {
					return conf
				}
			}
		}
	}

	if result.Status.State == entities.TaskStateInputRequired {
		return &entities.ADKConfirmation{
			Type: entities.ConfirmationTypeQuestion,
		}
	}

	return nil
}

// ParseADKConfirmation parses a raw DataPart map into a structured ADKConfirmation.
func ParseADKConfirmation(data map[string]any) *entities.ADKConfirmation {
	name, _ := data["name"].(string)
	if name != "adk_request_confirmation" {
		return nil
	}

	args, _ := data["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}

	funcCall, _ := args["originalFunctionCall"].(map[string]any)
	if funcCall == nil {
		funcCall = map[string]any{}
	}

	toolName, _ := funcCall["name"].(string)
	toolArgs, _ := funcCall["args"].(map[string]any)

	var hint string
	if toolConf, ok := args["toolConfirmation"].(map[string]any); ok {
		hint, _ = toolConf["hint"].(string)
	}

	if toolName == "ask_user" {
		questions := parseQuestions(toolArgs["questions"])
		return &entities.ADKConfirmation{
			Type:      entities.ConfirmationTypeAskUser,
			ToolName:  toolName,
			Questions: questions,
			Hint:      hint,
		}
	}

	return &entities.ADKConfirmation{
		Type:     entities.ConfirmationTypeApproval,
		ToolName: toolName,
		ToolArgs: toolArgs,
		Hint:     hint,
	}
}

func parseQuestions(raw any) []entities.QuestionItem {
	if raw == nil {
		return nil
	}

	if s, ok := raw.(string); ok {
		return []entities.QuestionItem{{Question: s}}
	}

	slice, ok := raw.([]any)
	if !ok {
		return nil
	}

	questions := make([]entities.QuestionItem, 0, len(slice))
	for _, item := range slice {
		switch v := item.(type) {
		case string:
			questions = append(questions, entities.QuestionItem{Question: v})
		case map[string]any:
			qText, _ := v["question"].(string)
			var choices []string
			if rawChoices, ok := v["choices"].([]any); ok {
				for _, c := range rawChoices {
					if cs, ok := c.(string); ok {
						choices = append(choices, cs)
					}
				}
			}
			questions = append(questions, entities.QuestionItem{
				Question: qText,
				Choices:  choices,
			})
		}
	}
	return questions
}