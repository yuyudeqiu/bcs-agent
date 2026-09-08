package web

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type diagnosisAgent struct {
	input *adk.AgentInput
}

func (a *diagnosisAgent) Name(context.Context) string        { return "diagnosis-test" }
func (a *diagnosisAgent) Description(context.Context) string { return "diagnosis test agent" }
func (a *diagnosisAgent) Run(_ context.Context, input *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	a.input = input
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	generator.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		IsStreaming: true,
		Role:        schema.Assistant,
		MessageStream: schema.StreamReaderFromArray([]*schema.Message{
			schema.AssistantMessage("诊断", nil),
			schema.AssistantMessage("完成", nil),
		}),
	}}})
	generator.Close()
	return iterator
}

func TestDiagnoserScopesPromptAndStreamsText(t *testing.T) {
	agent := &diagnosisAgent{}
	diagnose := NewDiagnoser(agent)
	var events []DiagnosisEvent
	err := diagnose(context.Background(), DiagnosisRequest{
		ClusterID: "BCS-K8S-40890", Namespace: "production", Kind: "Pod", Name: "web-1",
	}, func(event DiagnosisEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.input == nil || len(agent.input.Messages) != 1 {
		t.Fatalf("agent input = %#v", agent.input)
	}
	prompt := agent.input.Messages[0].Content
	for _, expected := range []string{"BCS-K8S-40890", "production", "web-1", "只读故障诊断"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt does not contain %q: %s", expected, prompt)
		}
	}
	if len(events) != 2 || events[0].Type != "text_delta" || events[0].Content != "诊断" || events[1].Content != "完成" {
		t.Fatalf("events = %#v", events)
	}
}
