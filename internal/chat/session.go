package chat

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type EventType string

const (
	EventTextDelta  EventType = "text_delta"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventApproval   EventType = "approval_required"
)

// Event 是 CLI 和未来 Web/SSE 共同消费的流式事件。
type Event struct {
	Type       EventType
	Content    string
	ToolName   string
	Arguments  string
	ApprovalID string
}

type EventHandler func(Event) error

type Session struct {
	mu         sync.Mutex
	runner     *adk.Runner
	store      *memoryCheckPointStore
	history    []*schema.Message
	pending    *pendingTurn
	turnNumber uint64
}

func NewSession(agent adk.Agent) *Session {
	store := &memoryCheckPointStore{values: make(map[string][]byte)}
	return &Session{
		runner: adk.NewRunner(context.Background(), adk.RunnerConfig{
			Agent: agent, EnableStreaming: true, CheckPointStore: store,
		}),
		store: store,
	}
}

type Approval struct {
	ID      string
	Content string
}

type pendingTurn struct {
	checkpointID string
	approvals    []Approval
	messages     []*schema.Message
}

type memoryCheckPointStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *memoryCheckPointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[id]
	return append([]byte(nil), value...), ok, nil
}

func (s *memoryCheckPointStore) Set(_ context.Context, id string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[id] = append([]byte(nil), value...)
	return nil
}

func (s *memoryCheckPointStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, id)
	return nil
}

func (s *Session) AskStream(ctx context.Context, question string, emit EventHandler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		return fmt.Errorf("当前操作仍在等待确认")
	}

	userMessage := schema.UserMessage(question)
	messages := append(append([]*schema.Message(nil), s.history...), userMessage)
	s.turnNumber++
	checkpointID := fmt.Sprintf("session-turn-%d", s.turnNumber)
	iterator := s.runner.Run(ctx, messages, adk.WithCheckPointID(checkpointID))
	turnMessages := []*schema.Message{userMessage}
	approvals, err := consumeAgentEvents(iterator, &turnMessages, emit)
	if err != nil {
		_ = s.store.Delete(ctx, checkpointID)
		return err
	}
	if len(approvals) > 0 {
		s.pending = &pendingTurn{checkpointID: checkpointID, approvals: approvals, messages: turnMessages}
		return nil
	}
	s.history = append(s.history, turnMessages...)
	_ = s.store.Delete(ctx, checkpointID)
	return nil
}

func consumeAgentEvents(iterator *adk.AsyncIterator[*adk.AgentEvent], turnMessages *[]*schema.Message, emit EventHandler) ([]Approval, error) {
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return nil, fmt.Errorf("执行 Agent: %w", event.Err)
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			approvals := make([]Approval, 0)
			for _, interrupt := range event.Action.Interrupted.InterruptContexts {
				if !interrupt.IsRootCause {
					continue
				}
				content, ok := interrupt.Info.(string)
				if !ok || content == "" || interrupt.ID == "" {
					return nil, fmt.Errorf("Agent 返回了无法识别的确认请求")
				}
				approval := Approval{ID: interrupt.ID, Content: content}
				approvals = append(approvals, approval)
				if err := emitEvent(emit, Event{Type: EventApproval, Content: content, ApprovalID: interrupt.ID}); err != nil {
					return nil, err
				}
			}
			if len(approvals) == 0 {
				return nil, fmt.Errorf("Agent 中断但没有可确认的根操作")
			}
			return approvals, nil
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		message, err := consumeMessageOutput(event.Output.MessageOutput, emit)
		if err != nil {
			return nil, err
		}
		if message != nil {
			*turnMessages = append(*turnMessages, message)
		}
	}
	return nil, nil
}

func (s *Session) PendingApprovals() []Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return nil
	}
	return append([]Approval(nil), s.pending.approvals...)
}

func (s *Session) ResumeApprovals(ctx context.Context, decisions map[string]bool, emit EventHandler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return fmt.Errorf("当前没有等待确认的操作")
	}
	if len(decisions) != len(s.pending.approvals) {
		return fmt.Errorf("必须为每个等待中的操作提供确认结果")
	}
	for _, approval := range s.pending.approvals {
		if _, ok := decisions[approval.ID]; !ok {
			return fmt.Errorf("缺少操作 %s 的确认结果", approval.ID)
		}
	}
	pending := s.pending
	s.pending = nil
	targets := make(map[string]any, len(decisions))
	for id, approved := range decisions {
		targets[id] = approved
	}
	iterator, err := s.runner.ResumeWithParams(ctx, pending.checkpointID, &adk.ResumeParams{Targets: targets})
	if err != nil {
		_ = s.store.Delete(ctx, pending.checkpointID)
		return fmt.Errorf("恢复 Agent: %w", err)
	}
	approvals, err := consumeAgentEvents(iterator, &pending.messages, emit)
	if err != nil {
		_ = s.store.Delete(ctx, pending.checkpointID)
		return err
	}
	if len(approvals) > 0 {
		pending.approvals = approvals
		s.pending = pending
		return nil
	}
	s.history = append(s.history, pending.messages...)
	_ = s.store.Delete(ctx, pending.checkpointID)
	return nil
}

func consumeMessageOutput(output *adk.MessageVariant, emit EventHandler) (*schema.Message, error) {
	if !output.IsStreaming {
		if output.Message == nil {
			return nil, nil
		}
		if err := emitMessage(output.Role, output.ToolName, output.Message, true, emit); err != nil {
			return nil, err
		}
		return output.Message, nil
	}
	if output.MessageStream == nil {
		return nil, nil
	}

	stream := output.MessageStream
	defer stream.Close()

	var chunks []*schema.Message
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("读取模型输出流: %w", err)
		}
		chunks = append(chunks, chunk)
		if output.Role == schema.Assistant && chunk.Content != "" {
			if err := emitEvent(emit, Event{Type: EventTextDelta, Content: chunk.Content}); err != nil {
				return nil, err
			}
		}
	}

	if len(chunks) == 0 {
		return nil, nil
	}
	message, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("合并模型输出流: %w", err)
	}
	// 文本已逐块发送，这里只发送合并后才完整的工具调用或工具结果。
	if err := emitMessage(output.Role, output.ToolName, message, false, emit); err != nil {
		return nil, err
	}
	return message, nil
}

func emitMessage(role schema.RoleType, toolName string, message *schema.Message, includeText bool, emit EventHandler) error {
	if includeText && role == schema.Assistant && message.Content != "" {
		if err := emitEvent(emit, Event{Type: EventTextDelta, Content: message.Content}); err != nil {
			return err
		}
	}
	for _, call := range message.ToolCalls {
		if err := emitEvent(emit, Event{
			Type:      EventToolCall,
			ToolName:  call.Function.Name,
			Arguments: call.Function.Arguments,
		}); err != nil {
			return err
		}
	}
	if role == schema.Tool {
		return emitEvent(emit, Event{
			Type:     EventToolResult,
			ToolName: toolName,
			Content:  message.Content,
		})
	}
	return nil
}

func emitEvent(emit EventHandler, event Event) error {
	if emit == nil {
		return nil
	}
	if err := emit(event); err != nil {
		return fmt.Errorf("发送流式事件: %w", err)
	}
	return nil
}

func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = nil
	if s.pending != nil {
		_ = s.store.Delete(context.Background(), s.pending.checkpointID)
		s.pending = nil
	}
}
