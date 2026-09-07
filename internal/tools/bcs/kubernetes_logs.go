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

type KubernetesLogsTool struct{ client kubeclient.Client }

func (t *KubernetesLogsTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "kubernetes_logs",
		Desc: "通过 BCS 网关只读获取单个容器的日志快照，用于排查应用报错或重启；需要 Pod 和 pods/log 读取权限。目标不明确时先用查询工具确定。\n" +
			"结果包含时间戳，不支持 follow、无限日志或分页。整个 JSON 最多 32 KiB；truncated=true 表示额外裁剪，可能缺失行或行尾，应减小 tail_lines 或 since_seconds，不能称为全部日志。" +
			"redacted=true 表示凭证已替换，仅识别已知网关 Token 及常见凭证模式。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"cluster_id":    {Type: schema.String, Required: true, Desc: "明确的 BCS 集群 ID，不是集群名称"},
			"namespace":     {Type: schema.String, Required: true, Desc: "Pod 所在的明确命名空间"},
			"pod":           {Type: schema.String, Required: true, Desc: "精确 Pod 名称，不是 Deployment 名称"},
			"container":     {Type: schema.String, Desc: "容器精确名称，支持普通、init 和临时容器；仅单容器可省略，多容器需明确选择，不能擅自选取返回的候选"},
			"tail_lines":    {Type: schema.Integer, Desc: "最近行数，默认 200，范围 1–1000"},
			"since_seconds": {Type: schema.Integer, Desc: "仅取最近多少秒的日志，正整数；与 tail_lines 同时生效，省略则仅限制行数"},
			"previous":      {Type: schema.Boolean, Desc: "可选，默认 false；true 读取上一次容器实例日志，适合排查重启，不存在时返回错误"},
		}),
	}, nil
}

func (t *KubernetesLogsTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	var request kubeclient.LogsRequest
	if err := utils.DecodeJSONStrict(arguments, &request); err != nil {
		return "", fmt.Errorf("解析 kubernetes_logs 参数: %w", err)
	}
	if err := request.NormalizeAndValidate(); err != nil {
		return "", err
	}
	result, err := t.client.Logs(ctx, request)
	if err != nil {
		return "", fmt.Errorf("查询容器日志: %w", err)
	}
	output, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("序列化日志结果: %w", err)
	}
	return string(output), nil
}
