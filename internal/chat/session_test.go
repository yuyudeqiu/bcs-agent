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

type approvalAgent struct {
	decision bool
}

func (a *approvalAgent) Name(context.Context) string        { return "approval-test" }
func (a *approvalAgent) Description(context.Context) string { return "approval test agent" }
func (a *approvalAgent) Run(ctx context.Context, _ *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	generator.Send(adk.StatefulInterrupt(ctx, `{"cluster_id":"c1","kind":"StatefulSet","namespace":"default","name":"db","current_replicas":1,"target_replicas":2}`, "saved-plan"))
	generator.Close()
	return iterator
}
func (a *approvalAgent) Resume(_ context.Context, info *adk.ResumeInfo, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	if decision, ok := info.ResumeData.(bool); ok {
		a.decision = decision
	}
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	generator.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant, Message: schema.AssistantMessage("已处理确认", nil),
	}}})
	generator.Close()
	return iterator
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

func TestSessionInterruptAndResumeApproval(t *testing.T) {
	agent := &approvalAgent{}
	session := NewSession(agent)
	var events []Event
	emit := func(event Event) error {
		events = append(events, event)
		return nil
	}
	if err := session.AskStream(context.Background(), "扩容 db", emit); err != nil {
		t.Fatal(err)
	}
	approvals := session.PendingApprovals()
	if len(approvals) != 1 || approvals[0].ID == "" || len(events) != 1 || events[0].Type != EventApproval {
		t.Fatalf("approvals=%+v events=%+v", approvals, events)
	}
	if err := session.AskStream(context.Background(), "另一个问题", nil); err == nil {
		t.Fatal("accepted a new turn while approval was pending")
	}
	if err := session.ResumeApprovals(context.Background(), map[string]bool{approvals[0].ID: false}, emit); err != nil {
		t.Fatal(err)
	}
	if agent.decision || len(session.PendingApprovals()) != 0 {
		t.Fatalf("decision=%v pending=%+v", agent.decision, session.PendingApprovals())
	}
	if len(events) != 2 || events[1].Type != EventTextDelta || events[1].Content != "已处理确认" {
		t.Fatalf("events=%+v", events)
	}
}
