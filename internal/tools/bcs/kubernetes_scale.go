package bcs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
	"github.com/yuyudeqiu/bcs-agent/internal/utils"
)

type KubernetesScaleTool struct{ client kubeclient.Client }

type scaleInterruptedState struct {
	Plan kubeclient.ScalePlan `json:"plan"`
}

type scaleApproval struct {
	ClusterID       string `json:"cluster_id"`
	Kind            string `json:"kind"`
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	CurrentReplicas int32  `json:"current_replicas"`
	TargetReplicas  int32  `json:"target_replicas"`
}

type scaleCancelledResult struct {
	Status string        `json:"status"`
	Target scaleApproval `json:"target"`
}

type scaleUnchangedResult struct {
	Status    string        `json:"status"`
	Submitted bool          `json:"submitted"`
	Target    scaleApproval `json:"target"`
}

func (t *KubernetesScaleTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "kubernetes_scale",
		Desc: "通过目标资源的 Kubernetes /scale 子资源修改副本数。仅操作一个明确的命名空间级资源；支持 Deployment、StatefulSet 以及 Discovery 中实际提供 scale 子资源的其他工作负载或 CRD。" +
			"调用后程序会读取当前副本数并要求用户确认，确认绑定目标、当前副本数和 resourceVersion；资源在确认期间变化时拒绝执行。一次只调用一个写操作，不并行发起多个变更。" +
			"返回 submitted=true 仅表示更新请求已被 API 接受；converged 只比较 Scale 的 observed replicas，不代表 Pod 已 Ready，需要时再用 kubernetes_query 查询工作负载状态。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"cluster_id": {Type: schema.String, Required: true, Desc: "明确的 BCS 集群 ID，不是名称"},
			"kind":       {Type: schema.String, Desc: "通常必填，资源 Kind，如 Deployment、StatefulSet 或支持 scale 的 CRD Kind；大小写不敏感。传 gvr 时可省略"},
			"gvr": {Type: schema.Object, Desc: "仅 Kind 有歧义或用户指定版本时提供完整主资源 GVR，不能传 deployments/scale 等子资源；同时传 kind 时必须匹配", SubParams: map[string]*schema.ParameterInfo{
				"group":    {Type: schema.String, Required: true, Desc: "API Group"},
				"version":  {Type: schema.String, Required: true, Desc: "API Version，如 v1"},
				"resource": {Type: schema.String, Required: true, Desc: "复数主资源名，如 deployments、statefulsets"},
			}},
			"namespace": {Type: schema.String, Required: true, Desc: "资源所在的明确命名空间"},
			"name":      {Type: schema.String, Required: true, Desc: "资源精确名称"},
			"replicas":  {Type: schema.Integer, Required: true, Desc: "目标副本数，最小 0；缩容到 0 会停止该工作负载的全部副本"},
		}),
	}, nil
}

func (t *KubernetesScaleTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	wasInterrupted, hasState, encodedState := tool.GetInterruptState[string](ctx)
	if !wasInterrupted {
		var request kubeclient.ScaleRequest
		if err := utils.DecodeJSONStrict(arguments, &request); err != nil {
			return "", fmt.Errorf("解析 kubernetes_scale 参数: %w", err)
		}
		if err := request.NormalizeAndValidate(); err != nil {
			return "", err
		}
		plan, err := t.client.PrepareScale(ctx, request)
		if err != nil {
			return "", fmt.Errorf("准备 Kubernetes 扩缩容: %w", err)
		}
		if plan.CurrentReplicas == plan.TargetReplicas {
			output, err := json.Marshal(scaleUnchangedResult{Status: "already_at_target", Submitted: false, Target: newScaleApproval(plan)})
			if err != nil {
				return "", fmt.Errorf("序列化扩缩容检查结果: %w", err)
			}
			return string(output), nil
		}
		stateJSON, err := json.Marshal(scaleInterruptedState{Plan: plan})
		if err != nil {
			return "", fmt.Errorf("保存扩缩容确认状态: %w", err)
		}
		approvalJSON, err := json.Marshal(newScaleApproval(plan))
		if err != nil {
			return "", fmt.Errorf("生成扩缩容确认信息: %w", err)
		}
		return "", tool.StatefulInterrupt(ctx, string(approvalJSON), string(stateJSON))
	}
	if !hasState {
		return "", fmt.Errorf("扩缩容恢复时缺少已确认的操作状态")
	}
	var state scaleInterruptedState
	if err := utils.DecodeJSONStrict(encodedState, &state); err != nil {
		return "", fmt.Errorf("解析扩缩容确认状态: %w", err)
	}
	isTarget, hasDecision, approved := tool.GetResumeContext[bool](ctx)
	if !isTarget {
		approvalJSON, _ := json.Marshal(newScaleApproval(state.Plan))
		return "", tool.StatefulInterrupt(ctx, string(approvalJSON), encodedState)
	}
	if !hasDecision {
		return "", fmt.Errorf("扩缩容恢复时缺少用户确认结果")
	}
	if !approved {
		output, err := json.Marshal(scaleCancelledResult{Status: "cancelled_by_user", Target: newScaleApproval(state.Plan)})
		if err != nil {
			return "", err
		}
		return string(output), nil
	}
	result, err := t.client.ApplyScale(ctx, state.Plan)
	if err != nil {
		return "", fmt.Errorf("执行 Kubernetes 扩缩容: %w", err)
	}
	output, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("序列化扩缩容结果: %w", err)
	}
	return string(output), nil
}

func newScaleApproval(plan kubeclient.ScalePlan) scaleApproval {
	return scaleApproval{
		ClusterID: plan.ClusterID, Kind: plan.Kind, Namespace: plan.Namespace, Name: plan.Name,
		CurrentReplicas: plan.CurrentReplicas, TargetReplicas: plan.TargetReplicas,
	}
}
