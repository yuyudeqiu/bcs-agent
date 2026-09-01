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
)

// Event 是 CLI 和未来 Web/SSE 共同消费的流式事件。
type Event struct {
	Type      EventType
	Content   string
	ToolName  string
	Arguments string
}

type EventHandler func(Event) error

type Session struct {
	mu      sync.Mutex
	agent   adk.Agent
	history []*schema.Message
}

func NewSession(agent adk.Agent) *Session {
	return &Session{agent: agent}
}

func (s *Session) AskStream(ctx context.Context, question string, emit EventHandler) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	userMessage := schema.UserMessage(question)
	messages := append(append([]*schema.Message(nil), s.history...), userMessage)
	iterator := s.agent.Run(ctx, &adk.AgentInput{
		Messages:        messages,
		EnableStreaming: true,
	})

	turnMessages := []*schema.Message{userMessage}
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return fmt.Errorf("执行 Agent: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		message, err := consumeMessageOutput(event.Output.MessageOutput, emit)
		if err != nil {
			return err
		}
		if message != nil {
			turnMessages = append(turnMessages, message)
		}
	}

	s.history = append(s.history, turnMessages...)
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
}
