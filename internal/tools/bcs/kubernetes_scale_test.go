package bcs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/yuyudeqiu/bcs-agent/internal/chat"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

type scaleClient struct {
	kubeclient.Client
	prepareCalls int
	applyCalls   int
	request      kubeclient.ScaleRequest
	plan         kubeclient.ScalePlan
	result       kubeclient.ScaleResult
	err          error
}

func (c *scaleClient) PrepareScale(_ context.Context, request kubeclient.ScaleRequest) (kubeclient.ScalePlan, error) {
	c.prepareCalls++
	c.request = request
	return c.plan, c.err
}

func (c *scaleClient) ApplyScale(_ context.Context, plan kubeclient.ScalePlan) (kubeclient.ScaleResult, error) {
	c.applyCalls++
	return c.result, c.err
}

type scaleApprovalModel struct {
	step       int
	sawResult  bool
	wantCancel bool
}

func (m *scaleApprovalModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *scaleApprovalModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.step++
	if m.step == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "scale-1", Type: "function", Function: schema.FunctionCall{
				Name: "kubernetes_scale", Arguments: `{"cluster_id":"BCS-K8S-10001","kind":"StatefulSet","namespace":"default","name":"db","replicas":3}`,
			},
		}}), nil
	}
	for _, message := range messages {
		if message.Role != schema.Tool || message.ToolCallID != "scale-1" {
			continue
		}
		cancelled := strings.Contains(message.Content, `"status":"cancelled_by_user"`)
		submitted := strings.Contains(message.Content, `"submitted":true`)
		m.sawResult = (m.wantCancel && cancelled) || (!m.wantCancel && submitted)
	}
	if !m.sawResult {
		return nil, errors.New("model did not receive expected scale result")
	}
	return schema.AssistantMessage("扩缩容流程结束。", nil), nil
}
func (m *scaleApprovalModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func TestKubernetesScaleApprovalControlsExecution(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "reject", true: "approve"}[approved], func(t *testing.T) {
			plan := kubeclient.ScalePlan{
				ClusterID: "BCS-K8S-10001", Kind: "StatefulSet", GVR: kubeclient.ResourceRef{Group: "apps", Version: "v1", Resource: "statefulsets"},
				Namespace: "default", Name: "db", CurrentReplicas: 1, TargetReplicas: 3, ResourceVersion: "7", UID: "uid-1", Source: "kubernetes",
			}
			client := &scaleClient{plan: plan, result: kubeclient.ScaleResult{
				ClusterID: plan.ClusterID, Kind: plan.Kind, GVR: plan.GVR, Namespace: plan.Namespace, Name: plan.Name,
				PreviousReplicas: 1, TargetReplicas: 3, ObservedReplicas: 1, Submitted: true, Source: "kubernetes",
			}}
			cm := &scaleApprovalModel{wantCancel: !approved}
			agent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
				Name: "scale-test", Model: cm,
				ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{&KubernetesScaleTool{client: client}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			session := chat.NewSession(agent)
			if err := session.AskStream(context.Background(), "扩容 db", nil); err != nil {
				t.Fatal(err)
			}
			approvals := session.PendingApprovals()
			if len(approvals) != 1 || client.prepareCalls != 1 || client.applyCalls != 0 {
				t.Fatalf("approvals=%+v client=%+v", approvals, client)
			}
			if err := session.ResumeApprovals(context.Background(), map[string]bool{approvals[0].ID: approved}, nil); err != nil {
				t.Fatal(err)
			}
			wantApply := 0
			if approved {
				wantApply = 1
			}
			if client.applyCalls != wantApply || cm.step != 2 || !cm.sawResult {
				t.Fatalf("apply=%d want=%d model=%+v", client.applyCalls, wantApply, cm)
			}
		})
	}
}

func TestKubernetesScaleToolPreparesBoundApproval(t *testing.T) {
	target := int32(3)
	client := &scaleClient{plan: kubeclient.ScalePlan{
		ClusterID: "BCS-K8S-10001", Kind: "StatefulSet", GVR: kubeclient.ResourceRef{Group: "apps", Version: "v1", Resource: "statefulsets"},
		Namespace: "default", Name: "db", CurrentReplicas: 1, TargetReplicas: target, ResourceVersion: "7", UID: "uid-1", Source: "kubernetes",
	}}
	scaleTool := &KubernetesScaleTool{client: client}
	_, err := scaleTool.InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-10001","kind":"StatefulSet","namespace":"default","name":"db","replicas":3}`)
	if err == nil {
		t.Fatal("expected approval interrupt")
	}
	// Tool interruption is converted to structured contexts by Eino's ToolsNode;
	// a direct invocation still lets us verify that preparation happened without applying.
	errorText := err.Error()
	if !strings.Contains(errorText, `"kind":"StatefulSet"`) || !strings.Contains(errorText, `"target_replicas":3`) {
		t.Fatalf("approval interrupt=%v", err)
	}
	if strings.Contains(strings.Split(errorText, "State=")[0], "resource_version") {
		t.Fatalf("internal precondition leaked into user-facing approval: %v", err)
	}
	if client.prepareCalls != 1 || client.applyCalls != 0 || client.request.Replicas == nil || *client.request.Replicas != 3 {
		t.Fatalf("client=%+v", client)
	}
}

func TestKubernetesScaleToolRejectsInvalidArgumentsBeforeClient(t *testing.T) {
	client := &scaleClient{err: errors.New("must not be called")}
	scaleTool := &KubernetesScaleTool{client: client}
	for _, arguments := range []string{
		`{}`, `null`, `{"cluster_id":"BCS-K8S-10001","kind":"Deployment","namespace":"default","name":"web"}`,
		`{"cluster_id":"BCS-K8S-10001","kind":"Deployment","namespace":"default","name":"web","replicas":-1}`,
		`{"cluster_id":"BCS-K8S-10001","kind":"Deployment","namespace":"default","name":"web","replicas":2,"approved":true}`,
	} {
		if _, err := scaleTool.InvokableRun(context.Background(), arguments); err == nil {
			t.Errorf("accepted %s", arguments)
		}
	}
	if client.prepareCalls != 0 || client.applyCalls != 0 {
		t.Fatalf("invalid arguments reached client: %+v", client)
	}
}

func TestKubernetesScaleToolSkipsUnchangedTarget(t *testing.T) {
	client := &scaleClient{plan: kubeclient.ScalePlan{
		ClusterID: "BCS-K8S-10001", Kind: "Deployment", GVR: kubeclient.ResourceRef{Group: "apps", Version: "v1", Resource: "deployments"},
		Namespace: "default", Name: "web", CurrentReplicas: 2, TargetReplicas: 2, ResourceVersion: "7", Source: "kubernetes",
	}}
	output, err := (&KubernetesScaleTool{client: client}).InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-10001","kind":"Deployment","namespace":"default","name":"web","replicas":2}`)
	if err != nil || !strings.Contains(output, `"status":"already_at_target"`) || !strings.Contains(output, `"submitted":false`) {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if client.prepareCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("client=%+v", client)
	}
}
