package bcs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
)

type GetClusterDetailTool struct {
	client bcsclient.Client
}

func (t *GetClusterDetailTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "get_cluster_detail",
		Desc: "根据集群 ID 获取某个集群的详细信息",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"cluster_id": {
				Type:     schema.String,
				Desc:     "集群 ID",
				Required: true,
			},
		}),
	}, nil
}

func (t *GetClusterDetailTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	var params struct {
		ClusterID string `json:"cluster_id"`
	}
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("解析 get_cluster_detail 参数: %w", err)
	}
	if params.ClusterID == "" {
		return "", fmt.Errorf("cluster_id 不能为空")
	}

	detail, err := t.client.GetCluster(ctx, params.ClusterID)
	if err != nil {
		return "", fmt.Errorf("查询集群 %s: %w", params.ClusterID, err)
	}
	result, err := json.Marshal(detail)
	if err != nil {
		return "", fmt.Errorf("序列化集群详情: %w", err)
	}
	return string(result), nil
}
