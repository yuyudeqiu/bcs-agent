package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
)

type QueryRequest struct {
	Output        string       `json:"output,omitempty"`
	ClusterID     string       `json:"cluster_id"`
	Action        string       `json:"action"`
	Kind          string       `json:"kind"`
	GVR           *ResourceRef `json:"gvr,omitempty"`
	Namespace     string       `json:"namespace,omitempty"`
	AllNamespaces bool         `json:"all_namespaces,omitempty"`
	Name          string       `json:"name,omitempty"`
	NameContains  string       `json:"name_contains,omitempty"`
	Limit         int64        `json:"limit,omitempty"`
	Continue      string       `json:"continue,omitempty"`
}

type QueryResult struct {
	Output           string            `json:"output"`
	ClusterID        string            `json:"cluster_id"`
	Action           string            `json:"action"`
	Kind             string            `json:"kind"`
	GVR              ResourceRef       `json:"gvr"`
	Namespace        string            `json:"namespace,omitempty"`
	AllNamespaces    bool              `json:"all_namespaces,omitempty"`
	NameContains     string            `json:"name_contains,omitempty"`
	ScannedCount     int               `json:"scanned_count,omitempty"`
	ScanLimitReached bool              `json:"scan_limit_reached,omitempty"`
	Source           string            `json:"source"`
	Items            []ResourceSummary `json:"items"`
	Count            int               `json:"count"`
	HasMore          bool              `json:"has_more"`
	Continue         string            `json:"continue,omitempty"`
}

type ResourceSummary struct {
	APIVersion string         `json:"api_version"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Namespace  string         `json:"namespace,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
	Resource   map[string]any `json:"resource,omitempty"`
	Redacted   bool           `json:"redacted,omitempty"`
}

type queryResource struct {
	groupVersion schema.GroupVersion
	namespaced   bool
}

// 只开放已确认的只读资源；不接受任意 GVR、子资源或 URL。
var queryResources = map[string]queryResource{
	"Pod":        {schema.GroupVersion{Version: "v1"}, true},
	"Namespace":  {schema.GroupVersion{Version: "v1"}, false},
	"Deployment": {schema.GroupVersion{Group: "apps", Version: "v1"}, true},
	"Node":       {schema.GroupVersion{Version: "v1"}, false},
	"Event":      {schema.GroupVersion{Version: "v1"}, true},
}

const maxNameScanPages = 20
const maxNameScanResources = 1000

var nameContainsPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

var kindPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// NormalizeAndValidate 同时供工具、真实客户端和 Mock 使用，校验发生在请求之前。
func (q *QueryRequest) NormalizeAndValidate() error {
	q.ClusterID = strings.TrimSpace(q.ClusterID)
	q.Namespace = strings.TrimSpace(q.Namespace)
	q.Name = strings.TrimSpace(q.Name)
	q.Kind = strings.TrimSpace(q.Kind)
	q.Action = strings.TrimSpace(q.Action)
	if err := validateClusterID(q.ClusterID); err != nil {
		return err
	}
	if q.GVR == nil {
		if !kindPattern.MatchString(q.Kind) {
			return fmt.Errorf("未指定 gvr 时必须提供格式有效的 kind")
		}
	} else {
		q.GVR.Group = strings.TrimSpace(q.GVR.Group)
		q.GVR.Version = strings.TrimSpace(q.GVR.Version)
		q.GVR.Resource = strings.TrimSpace(q.GVR.Resource)
		if q.Kind != "" && !kindPattern.MatchString(q.Kind) {
			return fmt.Errorf("kind 格式无效")
		}
		if q.GVR.Group != "" && len(validation.IsDNS1123Subdomain(q.GVR.Group)) != 0 {
			return fmt.Errorf("gvr.group 格式无效")
		}
		if len(validation.IsDNS1123Label(q.GVR.Version)) != 0 {
			return fmt.Errorf("gvr.version 格式无效")
		}
		if len(validation.IsDNS1123Subdomain(q.GVR.Resource)) != 0 {
			return fmt.Errorf("gvr.resource 格式无效")
		}
		if q.GVR.Group == "" && q.GVR.Resource == "secrets" {
			return fmt.Errorf("kubernetes_query 不允许读取 Secret")
		}
	}
	if q.Action != "list" && q.Action != "get" {
		return fmt.Errorf("action 仅支持 list 或 get")
	}
	if q.Output == "" {
		q.Output = "summary"
	}
	if q.Output != "summary" && q.Output != "full" {
		return fmt.Errorf("output 仅支持 summary 或 full")
	}
	if q.Output == "full" && q.Action != "get" {
		return fmt.Errorf("output=full 仅支持 get，必须指定单个资源的 name")
	}
	if q.Namespace != "" && len(validation.IsDNS1123Label(q.Namespace)) != 0 {
		return fmt.Errorf("namespace 格式无效")
	}
	if q.Name != "" && len(validation.IsDNS1123Subdomain(q.Name)) != 0 {
		return fmt.Errorf("name 格式无效")
	}
	if q.NameContains != "" {
		if q.Action != "list" {
			return fmt.Errorf("name_contains 仅支持 list；完整名称请用 get 和 name")
		}
		if len(q.NameContains) > 253 || !nameContainsPattern.MatchString(q.NameContains) {
			return fmt.Errorf("name_contains 必须是 1–253 字节的名称片段，只能包含字母、数字、点、下划线和连字符，不支持通配符或正则")
		}
	}
	if q.AllNamespaces && (q.Namespace != "" || q.Action != "list") {
		return fmt.Errorf("all_namespaces 仅用于 list，且不能同时指定 namespace")
	}
	if q.Action == "get" {
		if q.Name == "" {
			return fmt.Errorf("get 必须指定 name")
		}
		if q.Limit != 0 || q.Continue != "" {
			return fmt.Errorf("get 不接受 limit 或 continue")
		}
	} else {
		if q.Name != "" {
			return fmt.Errorf("list 不接受 name；查询指定资源请使用 get")
		}
		if q.Limit == 0 {
			q.Limit = 50
		}
		if q.Limit < 1 || q.Limit > 100 {
			return fmt.Errorf("limit 必须在 1 到 100 之间")
		}
		if len(q.Continue) > 16384 {
			return fmt.Errorf("continue 标记过长")
		}
	}
	return nil
}

func newQueryResult(q QueryRequest, source string) QueryResult {
	return QueryResult{Output: q.Output, ClusterID: q.ClusterID, Action: q.Action, Kind: q.Kind, GVR: *q.GVR, Namespace: q.Namespace, AllNamespaces: q.AllNamespaces, Source: source, NameContains: q.NameContains, Items: []ResourceSummary{}}
}

func (c *GatewayClient) Query(ctx context.Context, q QueryRequest) (QueryResult, error) {
	if err := q.NormalizeAndValidate(); err != nil {
		return QueryResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cfg, err := c.restConfig(q.ClusterID)
	if err != nil {
		return QueryResult{}, err
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return QueryResult{}, fmt.Errorf("创建 Kubernetes 查询客户端: %w", err)
	}
	core, err := c.coreClient(q.ClusterID)
	if err != nil {
		return QueryResult{}, err
	}
	resource, err := c.resolveResource(ctx, core, q)
	if err != nil {
		return QueryResult{}, err
	}
	if !resource.Namespaced && (q.Namespace != "" || q.AllNamespaces) {
		return QueryResult{}, fmt.Errorf("%s 是集群级资源，不能指定 namespace 或 all_namespaces", resource.Kind)
	}
	if resource.Namespaced && q.Namespace == "" && !q.AllNamespaces {
		return QueryResult{}, fmt.Errorf("%s 需要明确 namespace；跨命名空间列表查询请指定 all_namespaces=true", resource.Kind)
	}
	q.Kind = resource.Kind
	q.GVR = &ResourceRef{Group: resource.GVR.Group, Version: resource.GVR.Version, Resource: resource.GVR.Resource}
	gv := resource.GVR.GroupVersion()
	var endpoint dynamic.ResourceInterface = client.Resource(resource.GVR)
	if resource.Namespaced {
		endpoint = client.Resource(resource.GVR).Namespace(q.Namespace)
	}
	result := newQueryResult(q, "kubernetes")
	// 先校验每个对象身份，再匹配名称；未匹配项不生成摘要、更不会进入工具输出。
	appendItem := func(item unstructured.Unstructured) error {
		if item.GetName() == "" || item.GetKind() != q.Kind || item.GetAPIVersion() != gv.String() ||
			(q.Name != "" && item.GetName() != q.Name) ||
			(resource.Namespaced && item.GetNamespace() == "") ||
			(!resource.Namespaced && item.GetNamespace() != "") ||
			(q.Namespace != "" && item.GetNamespace() != q.Namespace) {
			return fmt.Errorf("Kubernetes 返回的资源身份与请求不一致或字段缺失")
		}
		if q.NameContains != "" && !strings.Contains(item.GetName(), q.NameContains) {
			return nil
		}
		summary, err := formatQueryResource(item, q.Output, c.cfg.APIToken)
		if err != nil {
			if q.Output == "full" {
				return err
			}
			return c.queryError(fmt.Sprintf("解析 %s 摘要", q.Kind), err)
		}
		result.Items = append(result.Items, summary)
		return nil
	}
	if q.Action == "get" {
		item, err := endpoint.Get(ctx, q.Name, metav1.GetOptions{})
		if err != nil {
			return QueryResult{}, c.queryError("获取 Kubernetes 资源", err)
		}
		if err := appendItem(*item); err != nil {
			return QueryResult{}, err
		}
	} else {
		options := metav1.ListOptions{Limit: q.Limit, Continue: q.Continue}
		seenTokens := map[string]bool{q.Continue: true}
		for pageIndex := 0; pageIndex < queryScanPageLimit(q); pageIndex++ {
			page, err := endpoint.List(ctx, options)
			if err != nil {
				return QueryResult{}, c.queryError("列出 Kubernetes 资源", err)
			}
			if page.GetKind() != q.Kind+"List" || page.GetAPIVersion() != gv.String() {
				return QueryResult{}, fmt.Errorf("Kubernetes 返回的列表类型与请求不一致")
			}
			// 每次完整处理服务端一页，保持 limit 不变，避免丢失分页标记之前的匹配项。
			if int64(len(page.Items)) > q.Limit {
				return QueryResult{}, fmt.Errorf("服务端未遵循 limit，无法安全返回完整分页")
			}
			for _, item := range page.Items {
				if item.GetKind() == "" {
					item.SetKind(q.Kind)
				}
				if item.GetAPIVersion() == "" {
					item.SetAPIVersion(gv.String())
				}
				if err := appendItem(item); err != nil {
					return QueryResult{}, err
				}
			}
			if q.NameContains != "" {
				result.ScannedCount += len(page.Items)
			}
			result.Continue = page.GetContinue()
			result.HasMore = result.Continue != ""
			if result.HasMore && seenTokens[result.Continue] {
				return QueryResult{}, fmt.Errorf("服务端返回重复 continue 标记")
			}
			if !result.HasMore || len(result.Items) > 0 || q.NameContains == "" {
				break
			}
			seenTokens[result.Continue] = true
			options.Continue = result.Continue
			result.ScanLimitReached = pageIndex+1 == queryScanPageLimit(q)
		}
	}

	result.Count = len(result.Items)
	return result, nil
}

// 名称过滤会跳过空匹配页，但同时限制页数和资源数，避免稀疏匹配无限扫描。
// limit 保持原样：改变服务端分页参数可能使 continue 无法继续使用。
func queryScanPageLimit(q QueryRequest) int {
	if q.NameContains == "" {
		return 1
	}
	return min(maxNameScanPages, maxNameScanResources/int(q.Limit))
}

// 保留原始错误链供 errors.Is/As 判断，但不把服务端错误正文或凭证传入模型。
type queryRequestError struct {
	operation string
	cause     error
}

func (e *queryRequestError) Error() string {
	if errors.Is(e.cause, context.Canceled) {
		return e.operation + "已取消"
	}
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return e.operation + "超时"
	}
	if status, ok := e.cause.(interface{ Status() metav1.Status }); ok {
		switch status.Status().Code {
		case 401:
			return e.operation + "失败（HTTP 401：身份验证失败）"
		case 403:
			return e.operation + "失败（HTTP 403：没有读取权限）"
		case 404:
			return e.operation + "失败（HTTP 404：目标资源或接口不存在）"
		case 410:
			return e.operation + "失败（HTTP 410：分页标记已过期，请从第一页重新查询）"
		}
		return fmt.Sprintf("%s失败（HTTP %d）", e.operation, status.Status().Code)
	}
	return e.operation + "失败（请求、连接或响应解析错误）"
}
func (e *queryRequestError) Unwrap() error { return e.cause }
func (c *GatewayClient) queryError(operation string, err error) error {
	return fmt.Errorf("%w", &queryRequestError{operation: operation, cause: err})
}
