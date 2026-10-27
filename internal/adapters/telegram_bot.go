package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"telegram-a2a/internal/entities"
	"telegram-a2a/internal/ports"
	"time"

	"github.com/lithammer/shortuuid/v4"
)

const (
	defaultTelegramAPIURL = "https://api.telegram.org"
	maxTelegramChunkSize  = 4000
)

// Telegram Bot API wire types

type tgAPIResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
}

type tgUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *tgMessage       `json:"message,omitempty"`
	CallbackQuery *tgCallbackQuery `json:"callback_query,omitempty"`
}

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from,omitempty"`
	Chat      tgChat  `json:"chat"`
	Text      string  `json:"text,omitempty"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message,omitempty"`
	Data    string     `json:"data,omitempty"`
}

type tgInlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type tgInlineKeyboardMarkup struct {
	InlineKeyboard [][]tgInlineKeyboardButton `json:"inline_keyboard"`
}

type tgBotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// TelegramBot implements long-polling Telegram Bot API and bridges messages to A2A.
type TelegramBot struct {
	token        string
	baseURL      string
	pollTimeout  time.Duration
	allowedUsers map[int64]struct{}
	httpClient   *http.Client
	a2aClient    ports.A2AClient
	sessionStore ports.SessionStore
	logger       ports.Logger

	mu         sync.Mutex
	cancelPoll context.CancelFunc
	doneChan   chan struct{}
}

var _ ports.Bot = (*TelegramBot)(nil)

func NewTelegramBot(
	cfg ports.Config,
	a2aClient ports.A2AClient,
	sessionStore ports.SessionStore,
	logger ports.Logger,
) (*TelegramBot, error) {
	cfg.SetDefault("apiURL", defaultTelegramAPIURL)
	cfg.SetDefault("pollTimeout", 30*time.Second)

	token := strings.TrimSpace(cfg.GetString("botToken"))
	if token == "" {
		return nil, errors.New("telegram botToken is required")
	}

	apiURL := strings.TrimRight(strings.TrimSpace(cfg.GetString("apiURL")), "/")
	pollTimeout := cfg.GetDuration("pollTimeout")
	if pollTimeout <= 0 {
		pollTimeout = 30 * time.Second
	}

	allowedSlice := cfg.GetInt64Slice("allowedUsers")
	allowedMap := make(map[int64]struct{}, len(allowedSlice))
	for _, id := range allowedSlice {
		allowedMap[id] = struct{}{}
	}

	return &TelegramBot{
		token:        token,
		baseURL:      fmt.Sprintf("%s/bot%s", apiURL, token),
		pollTimeout:  pollTimeout,
		allowedUsers: allowedMap,
		httpClient: &http.Client{
			Timeout: pollTimeout + 15*time.Second,
		},
		a2aClient:    a2aClient,
		sessionStore: sessionStore,
		logger:       logger,
	}, nil
}

func (b *TelegramBot) Start(ctx context.Context) error {
	b.mu.Lock()
	if b.cancelPoll != nil {
		b.mu.Unlock()
		return errors.New("telegram bot is already running")
	}
	pollCtx, cancel := context.WithCancel(ctx)
	b.cancelPoll = cancel
	done := make(chan struct{})
	b.doneChan = done
	b.mu.Unlock()

	defer func() {
		close(done)
		b.mu.Lock()
		b.cancelPoll = nil
		b.mu.Unlock()
	}()

	b.logger.Info("telegram bot polling started")
	b.logger.Debug("telegram bot polling configuration", ports.Fields{"pollTimeout": b.pollTimeout})

	b.registerCommands(pollCtx)

	var offset int64
	for {
		select {
		case <-pollCtx.Done():
			b.logger.Info("telegram bot polling stopped")
			return nil
		default:
		}

		updates, err := b.getUpdates(pollCtx, offset)
		if err != nil {
			if pollCtx.Err() != nil {
				return nil
			}
			b.logger.Error("error polling telegram updates", ports.Fields{"error": err})
			select {
			case <-pollCtx.Done():
				return nil
			case <-time.After(2 * time.Second):
				continue
			}
		}

		if len(updates) > 0 {
			b.logger.Debug("received telegram updates", ports.Fields{"count": len(updates)})
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			b.handleUpdate(pollCtx, u)
		}
	}
}

func (b *TelegramBot) Stop(ctx context.Context) error {
	b.mu.Lock()
	cancel := b.cancelPoll
	done := b.doneChan
	b.mu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()

	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (b *TelegramBot) HealthCheck(ctx context.Context) error {
	var me tgUser
	if err := b.callAPI(ctx, "getMe", nil, &me); err != nil {
		return fmt.Errorf("telegram getMe failed: %w", err)
	}
	return nil
}

func (b *TelegramBot) registerCommands(ctx context.Context) {
	expected := []tgBotCommand{
		{Command: "start", Description: "Show welcome and help message"},
		{Command: "new", Description: "Reset conversation session"},
		{Command: "status", Description: "Check connectivity to the A2A agent"},
	}

	var existing []tgBotCommand
	if err := b.callAPI(ctx, "getMyCommands", nil, &existing); err != nil {
		b.logger.Error("failed to get telegram bot commands", ports.Fields{"error": err})
		return
	}

	if commandsEqual(existing, expected) {
		b.logger.Debug("telegram bot commands already up to date")
		return
	}

	if err := b.callAPI(ctx, "deleteMyCommands", nil, nil); err != nil {
		b.logger.Error("failed to delete telegram bot commands", ports.Fields{"error": err})
		return
	}

	payload := map[string]any{
		"commands": expected,
	}
	if err := b.callAPI(ctx, "setMyCommands", payload, nil); err != nil {
		b.logger.Error("failed to register telegram bot commands", ports.Fields{"error": err})
	}
}

func commandsEqual(a, b []tgBotCommand) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Command != b[i].Command || a[i].Description != b[i].Description {
			return false
		}
	}
	return true
}

func (b *TelegramBot) isUserAllowed(userID int64) bool {
	if len(b.allowedUsers) == 0 {
		return true
	}
	_, ok := b.allowedUsers[userID]
	return ok
}

func (b *TelegramBot) handleUpdate(ctx context.Context, update tgUpdate) {
	b.logger.Debug("handling telegram update", ports.Fields{"update_id": update.UpdateID})
	if update.CallbackQuery != nil {
		b.handleCallbackQuery(ctx, update.CallbackQuery)
		return
	}
	if update.Message != nil && update.Message.Text != "" {
		b.handleMessage(ctx, update.Message)
	}
}

func (b *TelegramBot) handleMessage(ctx context.Context, msg *tgMessage) {
	var userID int64
	if msg.From != nil {
		userID = msg.From.ID
	}
	chatID := msg.Chat.ID

	if !b.isUserAllowed(userID) {
		b.logger.Warn("unauthorized telegram user", ports.Fields{"user_id": userID, "chat_id": chatID})
		_, _ = b.sendMessage(ctx, chatID, "Unauthorized user.", nil)
		return
	}

	text := strings.TrimSpace(msg.Text)
	b.logger.Debug("handling telegram message", ports.Fields{"user_id": userID, "chat_id": chatID, "text": text})
	switch {
	case text == "/start" || strings.HasPrefix(text, "/start@"):
		welcome := "Welcome to the A2A Telegram Gateway!\n\nAvailable commands:\n" +
			"/start - Show this help message\n" +
			"/new - Reset conversation session\n" +
			"/status - Check connectivity to the A2A agent\n\n" +
			"Send any message to chat with the agent."
		_, _ = b.sendMessage(ctx, chatID, welcome, nil)
		return

	case text == "/new" || strings.HasPrefix(text, "/new@"):
		b.sessionStore.DeleteContextID(userID)
		_, _ = b.sendMessage(ctx, chatID, "Conversation session reset. Your next message will start a fresh session.", nil)
		return

	case text == "/status" || strings.HasPrefix(text, "/status@"):
		if err := b.a2aClient.HealthCheck(ctx); err != nil {
			_, _ = b.sendMessage(ctx, chatID, fmt.Sprintf("Agent status: UNHEALTHY (%v)", err), nil)
			return
		}
		ctxID, hasCtx := b.sessionStore.GetContextID(userID)
		statusMsg := "Agent status: ONLINE"
		if hasCtx {
			statusMsg += fmt.Sprintf("\nActive contextId: %s", ctxID)
		} else {
			statusMsg += "\nActive contextId: none"
		}
		_, _ = b.sendMessage(ctx, chatID, statusMsg, nil)
		return
	}

	thinkingMsg, err := b.sendMessage(ctx, chatID, "Thinking...", nil)
	if err != nil {
		b.logger.Error("failed to send thinking message", ports.Fields{"error": err})
		return
	}

	contextID, _ := b.sessionStore.GetContextID(userID)
	result, err := b.a2aClient.SendMessage(ctx, text, contextID)
	if err != nil {
		b.logger.Error("error contacting a2a agent", ports.Fields{"error": err, "user_id": userID})
		_ = b.editMessageText(ctx, chatID, thinkingMsg.MessageID, fmt.Sprintf("Error contacting agent: %v", err), nil)
		return
	}

	if result.ContextID != "" {
		b.sessionStore.SetContextID(userID, result.ContextID)
	}

	b.renderA2AResult(ctx, chatID, userID, thinkingMsg.MessageID, result)
}

func (b *TelegramBot) handleCallbackQuery(ctx context.Context, cq *tgCallbackQuery) {
	_ = b.answerCallbackQuery(ctx, cq.ID)

	userID := cq.From.ID
	var chatID, messageID int64
	if cq.Message != nil {
		chatID = cq.Message.Chat.ID
		messageID = cq.Message.MessageID
	}

	if !b.isUserAllowed(userID) {
		return
	}

	parts := strings.SplitN(cq.Data, ":", 3)
	if len(parts) < 2 {
		return
	}
	action := parts[0]
	callbackID := parts[1]

	approval, ok := b.sessionStore.PopPendingApproval(callbackID)
	if !ok {
		if chatID != 0 && messageID != 0 {
			_ = b.editMessageText(ctx, chatID, messageID, "This action has expired or was already handled.", nil)
		}
		return
	}

	var replyText string
	var statusNotice string
	switch action {
	case "approve":
		statusNotice = "Approved. Processing..."
		replyText = "approved"
	case "reject":
		statusNotice = "Rejected."
		replyText = "rejected"
	case "choice":
		choiceVal := ""
		if len(parts) > 2 {
			choiceVal = parts[2]
		}
		statusNotice = fmt.Sprintf("Selected: %s", choiceVal)
		replyText = choiceVal
	default:
		return
	}

	if chatID != 0 && messageID != 0 {
		_ = b.editMessageText(ctx, chatID, messageID, statusNotice, nil)
	}

	result, err := b.a2aClient.SendMessage(ctx, replyText, approval.ContextID)
	if err != nil {
		b.logger.Error("error sending approval response to a2a", ports.Fields{"error": err})
		if chatID != 0 && messageID != 0 {
			_ = b.editMessageText(ctx, chatID, messageID, fmt.Sprintf("Error contacting agent: %v", err), nil)
		}
		return
	}

	if result.ContextID != "" {
		b.sessionStore.SetContextID(userID, result.ContextID)
	}

	b.renderA2AResult(ctx, chatID, userID, messageID, result)
}

func (b *TelegramBot) renderA2AResult(ctx context.Context, chatID, userID, thinkingMessageID int64, result *entities.TaskResult) {
	if result.Status.State == entities.TaskStateInputRequired {
		b.renderInputRequired(ctx, chatID, userID, thinkingMessageID, result)
		return
	}

	text := ExtractText(result)
	if strings.TrimSpace(text) == "" {
		text = "Agent returned no text response."
	}

	chunks := ChunkText(text, maxTelegramChunkSize)
	if len(chunks) == 0 {
		return
	}

	// Replace the "Thinking..." message with the first chunk
	_ = b.editMessageText(ctx, chatID, thinkingMessageID, chunks[0], nil)

	// Send any remaining chunks as follow-up messages
	for i := 1; i < len(chunks); i++ {
		_, _ = b.sendMessage(ctx, chatID, chunks[i], nil)
	}
}

func (b *TelegramBot) renderInputRequired(ctx context.Context, chatID, userID, thinkingMessageID int64, result *entities.TaskResult) {
	conf := ExtractADKConfirmation(result)
	explanation := ExtractText(result)

	contextID := result.ContextID
	if contextID == "" {
		contextID, _ = b.sessionStore.GetContextID(userID)
	}

	callbackID := shortuuid.New()[:8]

	var builder strings.Builder
	if explanation != "" {
		builder.WriteString(explanation)
		builder.WriteString("\n\n")
	}

	var markup *tgInlineKeyboardMarkup

	switch {
	case conf != nil && conf.Type == entities.ConfirmationTypeApproval:
		builder.WriteString(fmt.Sprintf("The agent wants to run: %s", conf.ToolName))
		if len(conf.ToolArgs) > 0 {
			builder.WriteString("\n")
			builder.WriteString(formatToolArgs(conf.ToolArgs))
		}
		if conf.Hint != "" {
			builder.WriteString(fmt.Sprintf("\nHint: %s", conf.Hint))
		}

		b.sessionStore.PutPendingApproval(callbackID, entities.PendingApproval{
			ContextID: contextID,
			UserID:    userID,
			ChatID:    chatID,
		})

		markup = &tgInlineKeyboardMarkup{
			InlineKeyboard: [][]tgInlineKeyboardButton{
				{
					{Text: "Approve", CallbackData: fmt.Sprintf("approve:%s", callbackID)},
					{Text: "Reject", CallbackData: fmt.Sprintf("reject:%s", callbackID)},
				},
			},
		}

	case conf != nil && conf.Type == entities.ConfirmationTypeAskUser:
		var buttons [][]tgInlineKeyboardButton
		for _, q := range conf.Questions {
			if q.Question != "" {
				builder.WriteString(q.Question)
				builder.WriteString("\n")
			}
			for _, choice := range q.Choices {
				buttons = append(buttons, []tgInlineKeyboardButton{
					{
						Text:         choice,
						CallbackData: fmt.Sprintf("choice:%s:%s", callbackID, choice),
					},
				})
			}
		}
		if len(buttons) > 0 {
			b.sessionStore.PutPendingApproval(callbackID, entities.PendingApproval{
				ContextID: contextID,
				UserID:    userID,
				ChatID:    chatID,
			})
			markup = &tgInlineKeyboardMarkup{InlineKeyboard: buttons}
		}

	default:
		if builder.Len() == 0 {
			builder.WriteString("The agent requires additional input. Please reply with your answer.")
		}
	}

	promptText := strings.TrimSpace(builder.String())
	chunks := ChunkText(promptText, maxTelegramChunkSize)
	if len(chunks) == 1 {
		_ = b.editMessageText(ctx, chatID, thinkingMessageID, chunks[0], markup)
		return
	}

	_ = b.editMessageText(ctx, chatID, thinkingMessageID, chunks[0], nil)
	for i := 1; i < len(chunks); i++ {
		var m *tgInlineKeyboardMarkup
		if i == len(chunks)-1 {
			m = markup
		}
		_, _ = b.sendMessage(ctx, chatID, chunks[i], m)
	}
}

func formatToolArgs(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s: %v", k, args[k]))
	}
	return strings.Join(lines, "\n")
}

// ChunkText splits text into chunks of at most maxLen runes, preferring newline boundaries.
func ChunkText(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = maxTelegramChunkSize
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	if len(runes) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(runes) > maxLen {
		splitIdx := maxLen
		for i := maxLen - 1; i > maxLen/2; i-- {
			if runes[i] == '\n' {
				splitIdx = i + 1
				break
			}
		}
		chunks = append(chunks, string(runes[:splitIdx]))
		runes = runes[splitIdx:]
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}

func (b *TelegramBot) getUpdates(ctx context.Context, offset int64) ([]tgUpdate, error) {
	payload := map[string]any{
		"offset":  offset,
		"timeout": int(b.pollTimeout.Seconds()),
	}
	var updates []tgUpdate
	if err := b.callAPI(ctx, "getUpdates", payload, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (b *TelegramBot) sendMessage(ctx context.Context, chatID int64, text string, markup *tgInlineKeyboardMarkup) (*tgMessage, error) {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	var msg tgMessage
	if err := b.callAPI(ctx, "sendMessage", payload, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (b *TelegramBot) editMessageText(ctx context.Context, chatID, messageID int64, text string, markup *tgInlineKeyboardMarkup) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	return b.callAPI(ctx, "editMessageText", payload, nil)
}

func (b *TelegramBot) answerCallbackQuery(ctx context.Context, callbackQueryID string) error {
	payload := map[string]any{
		"callback_query_id": callbackQueryID,
	}
	return b.callAPI(ctx, "answerCallbackQuery", payload, nil)
}

func (b *TelegramBot) callAPI(ctx context.Context, method string, payload any, out any) error {
	b.logger.Debug("telegram api request", ports.Fields{"method": method})
	url := fmt.Sprintf("%s/%s", b.baseURL, method)

	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal telegram %s request: %w", method, err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create telegram %s request: %w", method, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute telegram %s request: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read telegram %s response: %w", method, err)
	}

	var apiResp tgAPIResponse[json.RawMessage]
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return fmt.Errorf("decode telegram %s response (status %d): %w", method, resp.StatusCode, err)
	}

	if !apiResp.OK {
		return fmt.Errorf("telegram api %s error (%d): %s", method, apiResp.ErrorCode, apiResp.Description)
	}

	if out != nil && len(apiResp.Result) > 0 {
		if err := json.Unmarshal(apiResp.Result, out); err != nil {
			return fmt.Errorf("unmarshal telegram %s result: %w", method, err)
		}
	}

	b.logger.Debug("telegram api response", ports.Fields{"method": method, "ok": apiResp.OK})
	return nil
}
