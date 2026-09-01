package bcs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
)

type ListClustersTool struct {
	client bcsclient.Client
}

func (t *ListClustersTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "list_clusters",
		Desc: "列出指定项目下的所有 Kubernetes 集群，返回集群 ID、名称、状态和节点数",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"project_id": {
				Type:     schema.String,
				Desc:     "项目 ID",
				Required: true,
			},
		}),
	}, nil
}

func (t *ListClustersTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	var params struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("解析 list_clusters 参数: %w", err)
	}
	if params.ProjectID == "" {
		return "", fmt.Errorf("project_id 不能为空")
	}

	clusters, err := t.client.ListClusters(ctx, params.ProjectID)
	if err != nil {
		return "", fmt.Errorf("查询项目 %s 的集群: %w", params.ProjectID, err)
	}
	result, err := json.Marshal(clusters)
	if err != nil {
		return "", fmt.Errorf("序列化集群列表: %w", err)
	}
	return string(result), nil
}
