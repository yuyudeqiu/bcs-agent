package bcs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		Desc: "按项目 ID 查询集群列表，返回集群 ID、名称、状态、BCS 记录的 Kubernetes 版本和环境，接口也可能返回共享集群。版本和环境缺失时表示未知，不根据名称推断环境。未返回节点数时不能视为 0。项目 ID 未确定时先调用 list_projects。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"project_id": {
				Type:     schema.String,
				Desc:     "list_projects 返回的 projectID，不是项目名称或 projectCode",
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
	if strings.TrimSpace(params.ProjectID) == "" {
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
