package entities

// A2A JSON-RPC 2.0 protocol types for agent communication.

const (
	A2AMethodMessageSend = "message/send"

	TaskStateCompleted     = "completed"
	TaskStateInputRequired = "input-required"
	TaskStateFailed        = "failed"

	PartKindText = "text"
	PartKindData = "data"

	ConfirmationTypeApproval = "approval"
	ConfirmationTypeAskUser  = "ask_user"
	ConfirmationTypeQuestion = "question"
)

type JSONRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  MessageParams `json:"params"`
}

type MessageParams struct {
	Message A2AMessage `json:"message"`
}

type A2AMessage struct {
	Role      string    `json:"role"`
	Kind      string    `json:"kind"`
	MessageID string    `json:"messageId,omitempty"`
	ContextID string    `json:"contextId,omitempty"`
	Parts     []A2APart `json:"parts"`
}

type A2APart struct {
	Kind string         `json:"kind"`
	Text string         `json:"text,omitempty"`
	Data map[string]any `json:"data,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      any         `json:"id,omitempty"`
	Result  *TaskResult `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type TaskResult struct {
	ID        string       `json:"id,omitempty"`
	ContextID string       `json:"contextId,omitempty"`
	Status    TaskStatus   `json:"status"`
	Artifacts []Artifact   `json:"artifacts,omitempty"`
	History   []A2AMessage `json:"history,omitempty"`
}

type TaskStatus struct {
	State   string      `json:"state"`
	Message *A2AMessage `json:"message,omitempty"`
}

type Artifact struct {
	ArtifactID string    `json:"artifactId,omitempty"`
	Name       string    `json:"name,omitempty"`
	Parts      []A2APart `json:"parts"`
}

// QuestionItem represents a question posed by the ask_user tool.
type QuestionItem struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices,omitempty"`
}

// ADKConfirmation represents a parsed HITL confirmation from an input-required response.
type ADKConfirmation struct {
	Type      string         `json:"type"` // "approval", "ask_user", or "question"
	ToolName  string         `json:"tool_name,omitempty"`
	ToolArgs  map[string]any `json:"tool_args,omitempty"`
	Questions []QuestionItem `json:"questions,omitempty"`
	Hint      string         `json:"hint,omitempty"`
}

// PendingApproval stores state for an in-flight Telegram inline keyboard approval.
type PendingApproval struct {
	ContextID string
	UserID    int64
	ChatID    int64
}

// AgentCard represents the A2A discovery document at /.well-known/agent-card.json.
type AgentCard struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	Version     string `json:"version,omitempty"`
}
