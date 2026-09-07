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

type KubernetesQueryTool struct{ client kubeclient.Client }

func (t *KubernetesQueryTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "kubernetes_query",
		Desc: "通过 BCS 网关只读查询 Kubernetes 原生资源和 CRD，支持 list/get；不读取 Secret，日志使用 kubernetes_logs。直接查询目标 Kind，无需预先列出 CustomResourceDefinition。\n" +
			"结果说明：摘要省略字段不代表原资源没有该字段；redacted=true 表示凭证已替换，不能称为未经处理的原文。" +
			"count 仅为本次返回数量；scanned_count 为已扫描资源数，scan_limit_reached=true 表示达到扫描上限。" +
			"has_more=true 表示还有未扫描资源，不保证后续有匹配项；此时即使 count=0，也不能断言目标不存在或匹配已列全。\n" +
			"统计节点时使用 kind=Node，读取所有分页后汇总总数及 details.ready；仅 True 计为 Ready，False、Unknown 或缺失条件计为非 Ready，Ready 不代表可调度。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"cluster_id": {Type: schema.String, Required: true, Desc: "BCS 集群 ID，不是名称；不明确时先用 list_clusters 确定，无需 project_id"},
			"action":     {Type: schema.String, Required: true, Enum: []string{"list", "get"}, Desc: "list 分页查询；get 按精确 name 查询单个资源"},
			"output":     {Type: schema.String, Enum: []string{"summary", "full"}, Desc: "默认 summary。需要具体配置、镜像或同步状态等字段时，自行用 get + full，无需额外确认；仅本次有效。完整对象在 items[].resource，含 spec/status，凭证脱敏；上限 64 KiB，超限报错，不截断"},
			"kind":       {Type: schema.String, Desc: "资源完整 Kind，如 Pod、CronJob 或 CRD Kind，大小写不敏感；自动通过 Discovery 解析 preferred GVR 和规范 Kind。传 gvr 时可省略"},
			"gvr": {Type: schema.Object, Desc: "仅 Kind 有歧义或用户指定版本时提供完整 GVR；同时传 kind 时必须匹配", SubParams: map[string]*schema.ParameterInfo{
				"group":    {Type: schema.String, Required: true, Desc: "API Group；core/v1 资源使用空字符串"},
				"version":  {Type: schema.String, Required: true, Desc: "API Version，例如 v1 或 v1beta1"},
				"resource": {Type: schema.String, Required: true, Desc: "复数资源名，例如 pods、cronjobs、widgets"},
			}},
			"namespace":      {Type: schema.String, Desc: "命名空间级资源必填，跨命名空间 list 除外；Node、Namespace 等集群级资源不传"},
			"all_namespaces": {Type: schema.Boolean, Desc: "仅跨命名空间 list 时设为 true，不能同时指定 namespace"},
			"name":           {Type: schema.String, Desc: "get 必填的精确资源名称；list 不传"},
			"name_contains":  {Type: schema.String, Desc: "仅知道名称片段时用 list + 此参数，不要拉全量列表自行筛选；与 name 互斥。大小写敏感的字面包含匹配，不支持通配符；1–253 字节，限字母、数字、点、下划线和连字符，如 cwlicense。最多扫描 20 页、1000 个资源，找到含匹配项的一页即返回；省略则普通分页"},
			"limit":          {Type: schema.Integer, Desc: "list 服务端每页条数，也是本次匹配结果上限；默认 50，范围 1–100。get 不传"},
			"continue":       {Type: schema.String, Desc: "list 下一页标记，原样使用上次返回值，同时保持其他查询参数一致"},
		}),
	}, nil
}

func (t *KubernetesQueryTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	var request kubeclient.QueryRequest
	if err := utils.DecodeJSONStrict(arguments, &request); err != nil {
		return "", fmt.Errorf("解析 kubernetes_query 参数: %w", err)
	}
	if err := request.NormalizeAndValidate(); err != nil {
		return "", err
	}
	result, err := t.client.Query(ctx, request)
	if err != nil {
		return "", fmt.Errorf("查询 Kubernetes 资源: %w", err)
	}
	output, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("序列化 Kubernetes 查询结果: %w", err)
	}
	return string(output), nil
}
