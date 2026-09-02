package bcs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
)

type ListProjectsTool struct {
	client bcsclient.Client
}

func (t *ListProjectsTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "list_projects",
		Desc: "列出当前账号可见的所有 BCS 项目，返回项目 ID、名称和 code。需要确定 project_id 时优先调用。",
	}, nil
}

func (t *ListProjectsTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	projects, err := t.client.ListProjects(ctx)
	if err != nil {
		return "", fmt.Errorf("查询项目列表: %w", err)
	}
	result, err := json.Marshal(projects)
	if err != nil {
		return "", fmt.Errorf("序列化项目列表: %w", err)
	}
	return string(result), nil
}
