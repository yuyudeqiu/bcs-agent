package bcs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

type GetClusterNodeSummaryTool struct {
	client kubeclient.Client
}

func (t *GetClusterNodeSummaryTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "get_cluster_node_summary",
		Desc: "查询指定集群的节点总数、Ready 和非 Ready 数量。真实模式通过 BCS 网关读取 Kubernetes Node 列表；非 Ready 包含 Ready=False、Unknown 或缺失条件的节点。节点总数包括控制平面和工作节点，Ready 不代表可调度。source=mock 表示示例数据。仅需 cluster_id，不需要 project_id；集群 ID 不明确时先调用 list_clusters 确定。查询节点数量优先使用本工具。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"cluster_id": {
				Type:     schema.String,
				Desc:     "目标 BCS 集群 ID，例如 BCS-K8S-40888，不是集群名称",
				Required: true,
			},
		}),
	}, nil
}

func (t *GetClusterNodeSummaryTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	var params struct {
		ClusterID string `json:"cluster_id"`
	}
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("解析 get_cluster_node_summary 参数: %w", err)
	}
	params.ClusterID = strings.TrimSpace(params.ClusterID)
	if params.ClusterID == "" {
		return "", fmt.Errorf("cluster_id 不能为空")
	}
	summary, err := t.client.GetNodeSummary(ctx, params.ClusterID)
	if err != nil {
		return "", fmt.Errorf("查询节点概览: %w", err)
	}
	result, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("序列化节点概览: %w", err)
	}
	return string(result), nil
}
