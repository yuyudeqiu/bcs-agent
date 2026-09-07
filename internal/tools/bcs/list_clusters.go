package bcs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
	"github.com/yuyudeqiu/bcs-agent/internal/utils"
)

type ListClustersTool struct {
	client bcsclient.Client
}

func (t *ListClustersTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "list_clusters",
		Desc: "查询 BCS 集群列表以确定目标集群，可按项目过滤，结果可能包含共享集群。返回 ID、名称、状态及 BCS 记录的 Kubernetes 版本和环境；缺失字段表示未知，不根据名称推断环境，不将缺失节点数视为 0。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"project_id": {
				Type: schema.String,
				Desc: "未限定项目时省略或传空。限定项目时用 list_projects 返回的 projectID，不是名称或 projectCode；只有项目名称时先查 ID，无法确定则追问，不能改查全部",
			},
		}),
	}, nil
}

func (t *ListClustersTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	var params struct {
		ProjectID string `json:"project_id"`
	}
	if err := utils.DecodeJSONStrict(arguments, &params); err != nil {
		return "", fmt.Errorf("解析 list_clusters 参数: %w", err)
	}
	clusters, err := t.client.ListClusters(ctx, params.ProjectID)
	if err != nil {
		return "", fmt.Errorf("查询集群列表: %w", err)
	}
	result, err := json.Marshal(clusters)
	if err != nil {
		return "", fmt.Errorf("序列化集群列表: %w", err)
	}
	return string(result), nil
}
