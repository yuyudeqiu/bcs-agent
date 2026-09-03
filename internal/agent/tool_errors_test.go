package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kschema "k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yuyudeqiu/bcs-agent/internal/chat"
)

func TestQueryFailuresBecomeToolResults(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{"not found", fmt.Errorf("query: %w", apierrors.NewNotFound(kschema.GroupResource{Resource: "pods"}, "web")), "not_found"},
		{"forbidden", apierrors.NewForbidden(kschema.GroupResource{Resource: "pods"}, "web", errors.New("private-secret")), "forbidden"},
		{"BCS body", errors.New("查询项目: BCS 返回 HTTP 503: private-secret"), "api_error"},
		{"BCS business", errors.New("BCS 返回错误 code=123 message=private-secret"), "business_error"},
		{"validation", errors.New("get 必须指定 name"), "query_error"},
		{"request timeout", fmt.Errorf("request: %w", context.DeadlineExceeded), "timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := recoverQueryErrors(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
				return &compose.ToolOutput{Result: "partial"}, test.err
			})
			output, err := endpoint(context.Background(), &compose.ToolInput{Name: "kubernetes_query"})
			if err != nil {
				t.Fatal(err)
			}
			var result queryFailure
			if err := json.Unmarshal([]byte(output.Result), &result); err != nil {
				t.Fatal(err)
			}
			if result.OK || result.Error.Code != test.code || result.Error.Hint == "" || strings.Contains(output.Result, "private-secret") || strings.Contains(output.Result, "partial") {
				t.Fatalf("invalid failure envelope: %s", output.Result)
			}
		})
	}
}

func TestQueryControlSignalsAndSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	endpoint := recoverQueryErrors(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) { called = true; return nil, nil })
	if _, err := endpoint(ctx, &compose.ToolInput{Name: "kubernetes_query"}); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled call executed: %v", err)
	}
	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	if _, err := endpoint(deadlineCtx, &compose.ToolInput{Name: "kubernetes_query"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("outer deadline swallowed: %v", err)
	}
	for _, signal := range []error{context.Canceled, compose.NewInterruptAndRerunErr("approval required")} {
		endpoint := recoverQueryErrors(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) { return nil, signal })
		if _, err := endpoint(context.Background(), &compose.ToolInput{Name: "kubernetes_query"}); err != signal {
			t.Fatalf("control signal swallowed: %v", err)
		}
	}
	fatal := errors.New("write failed")
	endpoint = recoverQueryErrors(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) { return nil, fatal })
	if _, err := endpoint(context.Background(), &compose.ToolInput{Name: "future_write_tool"}); err != fatal {
		t.Fatalf("unregistered tool error swallowed: %v", err)
	}
	want := &compose.ToolOutput{Result: `[{"name":"web"}]`}
	endpoint = recoverQueryErrors(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) { return want, nil })
	if got, err := endpoint(context.Background(), &compose.ToolInput{Name: "kubernetes_query"}); err != nil || got != want {
		t.Fatalf("success modified: %+v %v", got, err)
	}
}

type recoveryTool struct{ name string }

func (t *recoveryTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "test query"}, nil
}
func (t *recoveryTool) InvokableRun(_ context.Context, args string, _ ...tool.Option) (string, error) {
	if t.name == "list_clusters" {
		return `[{"cluster_id":"test-cluster"}]`, nil
	}
	if strings.Contains(args, "missing") {
		return "", fmt.Errorf("查询 Kubernetes 资源: %w", apierrors.NewNotFound(kschema.GroupResource{Resource: "pods"}, "missing"))
	}
	return `{"name":"web","phase":"Running"}`, nil
}

type recoveryModel struct {
	toolName                                    string
	step                                        int
	sawFailure, sawParallelSuccess, sawRecovery bool
}

func (m *recoveryModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *recoveryModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.step++
	if m.step == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{
			{ID: "bad", Type: "function", Function: schema.FunctionCall{Name: m.toolName, Arguments: `{"name":"missing"}`}},
			{ID: "parallel", Type: "function", Function: schema.FunctionCall{Name: "list_clusters", Arguments: `{}`}},
		}), nil
	}
	for _, message := range messages {
		if message.Role != schema.Tool {
			continue
		}
		switch message.ToolCallID {
		case "bad":
			var failure queryFailure
			if err := json.Unmarshal([]byte(message.Content), &failure); err != nil {
				return nil, err
			}
			m.sawFailure = !failure.OK && failure.Error.Code == "not_found"
		case "parallel":
			m.sawParallelSuccess = strings.Contains(message.Content, "test-cluster")
		case "fixed":
			m.sawRecovery = strings.Contains(message.Content, "Running")
		}
	}
	if m.step == 2 {
		if !m.sawFailure || !m.sawParallelSuccess {
			return nil, errors.New("model did not receive both tool results")
		}
		return schema.AssistantMessage("核对目标后重新查询。", []schema.ToolCall{{ID: "fixed", Type: "function", Function: schema.FunctionCall{Name: m.toolName, Arguments: `{"name":"web"}`}}}), nil
	}
	if !m.sawRecovery {
		return nil, errors.New("model did not receive corrected query result")
	}
	return schema.AssistantMessage("已找到 web，状态 Running。", nil), nil
}
func (m *recoveryModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func TestStreamingAgentContinuesAfterQueryError(t *testing.T) {
	for _, name := range []string{"kubernetes_query", "kubernetes_logs"} {
		t.Run(name, func(t *testing.T) { testStreamingAgentRecovery(t, name) })
	}
}

func testStreamingAgentRecovery(t *testing.T, toolName string) {
	cm := &recoveryModel{toolName: toolName}
	agent, err := newWithModel(context.Background(), cm, []tool.BaseTool{&recoveryTool{name: toolName}, &recoveryTool{name: "list_clusters"}})
	if err != nil {
		t.Fatal(err)
	}
	session := chat.NewSession(agent)
	var text strings.Builder
	failedResults := 0
	err = session.AskStream(context.Background(), "查询 Pod", func(event chat.Event) error {
		if event.Type == chat.EventTextDelta {
			text.WriteString(event.Content)
		}
		if event.Type == chat.EventToolResult && strings.Contains(event.Content, `"ok":false`) {
			failedResults++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if cm.step != 3 || !cm.sawFailure || !cm.sawParallelSuccess || !cm.sawRecovery || failedResults != 1 || !strings.Contains(text.String(), "状态 Running") {
		t.Fatalf("loop did not recover: steps=%d failure=%v parallel=%v recovered=%v events=%d text=%s", cm.step, cm.sawFailure, cm.sawParallelSuccess, cm.sawRecovery, failedResults, text.String())
	}
}
