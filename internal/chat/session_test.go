package chat

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type streamingAgent struct {
	inputs []*adk.AgentInput
}

func (a *streamingAgent) Name(context.Context) string        { return "test" }
func (a *streamingAgent) Description(context.Context) string { return "test agent" }

func (a *streamingAgent) Run(_ context.Context, input *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	a.inputs = append(a.inputs, input)
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	generator.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		IsStreaming: true,
		Role:        schema.Assistant,
		MessageStream: schema.StreamReaderFromArray([]*schema.Message{
			schema.AssistantMessage("你", nil),
			schema.AssistantMessage("好", nil),
		}),
	}}})
	generator.Close()
	return iterator
}

func TestSessionAskStream(t *testing.T) {
	agent := &streamingAgent{}
	session := NewSession(agent)

	var contents []string
	err := session.AskStream(context.Background(), "第一次", func(event Event) error {
		if event.Type == EventTextDelta {
			contents = append(contents, event.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("AskStream() error = %v", err)
	}
	if len(contents) != 2 || contents[0] != "你" || contents[1] != "好" {
		t.Fatalf("unexpected stream chunks: %#v", contents)
	}
	if !agent.inputs[0].EnableStreaming {
		t.Fatal("Agent input did not enable streaming")
	}

	err = session.AskStream(context.Background(), "第二次", nil)
	if err != nil {
		t.Fatalf("second AskStream() error = %v", err)
	}
	if got := len(agent.inputs[1].Messages); got != 3 {
		t.Fatalf("second turn message count = %d, want 3", got)
	}
	if got := agent.inputs[1].Messages[1].Content; got != "你好" {
		t.Fatalf("history assistant message = %q, want %q", got, "你好")
	}
}
