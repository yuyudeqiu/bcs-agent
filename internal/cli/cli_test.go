package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/yuyudeqiu/bcs-agent/internal/chat"
)

type onceAgent struct {
	prompt   string
	approval bool
	decision bool
}

func (a *onceAgent) Name(context.Context) string        { return "once-test" }
func (a *onceAgent) Description(context.Context) string { return "once test agent" }
func (a *onceAgent) Run(ctx context.Context, input *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	a.prompt = input.Messages[len(input.Messages)-1].Content
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	if a.approval {
		generator.Send(adk.StatefulInterrupt(ctx, `{"cluster_id":"c1","kind":"Deployment","namespace":"default","name":"web","current_replicas":1,"target_replicas":2}`, "plan"))
	} else {
		generator.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
			Role: schema.Assistant, Message: schema.AssistantMessage("完成", nil),
		}}})
	}
	generator.Close()
	return iterator
}
func (a *onceAgent) Resume(_ context.Context, info *adk.ResumeInfo, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	a.decision, _ = info.ResumeData.(bool)
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	generator.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant, Message: schema.AssistantMessage("已执行", nil),
	}}})
	generator.Close()
	return iterator
}

func TestFormatApproval(t *testing.T) {
	got := formatApproval(`{"cluster_id":"BCS-K8S-10001","kind":"StatefulSet","namespace":"default","name":"db","current_replicas":1,"target_replicas":3}`)
	want := "集群 BCS-K8S-10001 的 StatefulSet default/db：副本数 1 → 3"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := formatApproval("unrecognized"); got != "unrecognized" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestRunOnce(t *testing.T) {
	agent := &onceAgent{}
	var output bytes.Buffer
	if err := RunOnce(context.Background(), strings.NewReader(""), &output, chat.NewSession(agent), " 查看项目列表 "); err != nil {
		t.Fatal(err)
	}
	if agent.prompt != "查看项目列表" {
		t.Fatalf("prompt = %q", agent.prompt)
	}
	if got := output.String(); got != "Agent> 完成\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunOnceApproval(t *testing.T) {
	agent := &onceAgent{approval: true}
	var output bytes.Buffer
	if err := RunOnce(context.Background(), strings.NewReader("y\n"), &output, chat.NewSession(agent), "扩容 web"); err != nil {
		t.Fatal(err)
	}
	if !agent.decision {
		t.Fatal("approval decision was not passed to the agent")
	}
	if !strings.Contains(output.String(), "确认执行此操作？[y/N]") || !strings.Contains(output.String(), "Agent> 已执行") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunOnceApprovalRequiresInput(t *testing.T) {
	agent := &onceAgent{approval: true}
	var output bytes.Buffer
	err := RunOnce(context.Background(), strings.NewReader(""), &output, chat.NewSession(agent), "扩容 web")
	if err == nil || !strings.Contains(err.Error(), "标准输入已关闭") {
		t.Fatalf("error = %v", err)
	}
	if agent.decision {
		t.Fatal("operation was approved without input")
	}
}
