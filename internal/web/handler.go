package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/yuyudeqiu/bcs-agent/internal/bcs"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

//go:embed *.html
var assets embed.FS

type Dependencies struct {
	BCS        bcs.Client
	Kubernetes kubeclient.Client
	Diagnose   DiagnoseFunc
}

type DiagnosisRequest struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

type DiagnosisEvent struct {
	Type     string `json:"type"`
	Content  string `json:"content,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
}

type DiagnoseFunc func(context.Context, DiagnosisRequest, func(DiagnosisEvent) error) error

type namespaceListResponse struct {
	Cluster      bcs.Cluster                  `json:"cluster"`
	NodeCount    int                          `json:"node_count"`
	ReadyNodes   int                          `json:"ready_nodes"`
	Namespaces   []kubeclient.ResourceSummary `json:"namespaces"`
	HasMore      bool                         `json:"has_more"`
	Continue     string                       `json:"continue,omitempty"`
	NodesHasMore bool                         `json:"nodes_has_more"`
}

type namespaceOverviewResponse struct {
	Cluster            bcs.Cluster                  `json:"cluster"`
	Namespace          kubeclient.ResourceSummary   `json:"namespace"`
	Pods               []kubeclient.ResourceSummary `json:"pods"`
	Deployments        []kubeclient.ResourceSummary `json:"deployments"`
	WarningEvents      []kubeclient.ResourceSummary `json:"warning_events"`
	PodsHasMore        bool                         `json:"pods_has_more"`
	DeploymentsHasMore bool                         `json:"deployments_has_more"`
	EventsHasMore      bool                         `json:"events_has_more"`
}

func NewHandler(dependencies Dependencies) http.Handler {
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.SetHTMLTemplate(template.Must(template.ParseFS(assets, "*.html")))

	engine.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})
	engine.GET("/clusters/:clusterID", func(c *gin.Context) {
		c.HTML(http.StatusOK, "cluster.html", gin.H{"ClusterID": c.Param("clusterID")})
	})
	engine.GET("/clusters/:clusterID/namespaces/:namespace", func(c *gin.Context) {
		c.HTML(http.StatusOK, "namespace.html", gin.H{"ClusterID": c.Param("clusterID"), "Namespace": c.Param("namespace")})
	})
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	engine.GET("/api/v1/clusters", func(c *gin.Context) {
		clusters, err := dependencies.BCS.ListClusters(c.Request.Context(), strings.TrimSpace(c.Query("project_id")))
		if err != nil {
			writeUpstreamError(c, "查询集群列表失败", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": clusters, "total": len(clusters)})
	})
	engine.GET("/api/v1/clusters/:clusterID/namespaces", func(c *gin.Context) {
		overview, status, err := loadNamespaces(c.Request.Context(), dependencies, c.Param("clusterID"), c.Query("continue"))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			message := err.Error()
			if status >= http.StatusInternalServerError {
				slog.Error("查询 Namespace 列表失败", "cluster_id", c.Param("clusterID"), "error", err)
				message = "查询 Namespace 列表失败"
			}
			c.JSON(status, gin.H{"error": gin.H{"code": "list_namespaces_failed", "message": message}})
			return
		}
		c.JSON(http.StatusOK, overview)
	})
	engine.GET("/api/v1/clusters/:clusterID/namespaces/:namespace/overview", func(c *gin.Context) {
		overview, status, err := loadNamespaceOverview(c.Request.Context(), dependencies, c.Param("clusterID"), c.Param("namespace"))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			message := err.Error()
			if status >= http.StatusInternalServerError {
				slog.Error("查询 Namespace 概览失败", "cluster_id", c.Param("clusterID"), "namespace", c.Param("namespace"), "error", err)
				message = "查询 Namespace 概览失败"
			}
			c.JSON(status, gin.H{"error": gin.H{"code": "namespace_overview_failed", "message": message}})
			return
		}
		c.JSON(http.StatusOK, overview)
	})
	engine.POST("/api/v1/diagnoses", func(c *gin.Context) {
		if dependencies.Diagnose == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "agent_unavailable", "message": "Agent 分析功能未配置"}})
			return
		}
		request, err := decodeDiagnosisRequest(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_request", "message": err.Error()}})
			return
		}

		c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
		c.Header("Cache-Control", "no-cache")
		c.Status(http.StatusOK)
		encoder := json.NewEncoder(c.Writer)
		emit := func(event DiagnosisEvent) error {
			if err := encoder.Encode(event); err != nil {
				return err
			}
			c.Writer.Flush()
			return nil
		}
		if err := dependencies.Diagnose(c.Request.Context(), request, emit); err != nil {
			slog.Error("Agent 分析失败", "cluster_id", request.ClusterID, "namespace", request.Namespace, "name", request.Name, "error", err)
			_ = emit(DiagnosisEvent{Type: "error", Content: "Agent 分析失败，请稍后重试"})
			return
		}
		_ = emit(DiagnosisEvent{Type: "done"})
	})

	return engine
}

func findCluster(ctx context.Context, client bcs.Client, clusterID string) (bcs.Cluster, int, error) {
	clusterID = strings.TrimSpace(clusterID)
	clusters, err := client.ListClusters(ctx, "")
	if err != nil {
		return bcs.Cluster{}, http.StatusBadGateway, fmt.Errorf("查询集群信息: %w", err)
	}
	for _, candidate := range clusters {
		if candidate.ID == clusterID {
			return candidate, http.StatusOK, nil
		}
	}
	return bcs.Cluster{}, http.StatusNotFound, fmt.Errorf("集群不存在")
}

func loadNamespaces(ctx context.Context, dependencies Dependencies, clusterID, continuation string) (namespaceListResponse, int, error) {
	cluster, status, err := findCluster(ctx, dependencies.BCS, clusterID)
	if err != nil {
		return namespaceListResponse{}, status, err
	}
	nodes, err := dependencies.Kubernetes.Query(ctx, kubeclient.QueryRequest{
		ClusterID: cluster.ID, Action: "list", Kind: "Node", Limit: 100,
	})
	if err != nil {
		return namespaceListResponse{}, http.StatusBadGateway, fmt.Errorf("查询 Node: %w", err)
	}
	namespaces, err := dependencies.Kubernetes.Query(ctx, kubeclient.QueryRequest{
		ClusterID: cluster.ID, Action: "list", Kind: "Namespace", Limit: 100, Continue: continuation,
	})
	if err != nil {
		return namespaceListResponse{}, http.StatusBadGateway, fmt.Errorf("查询 Namespace: %w", err)
	}
	result := namespaceListResponse{
		Cluster: cluster, NodeCount: nodes.Count, Namespaces: namespaces.Items,
		HasMore: namespaces.HasMore, Continue: namespaces.Continue, NodesHasMore: nodes.HasMore,
	}
	for _, node := range nodes.Items {
		if stringDetail(node, "ready") == "True" {
			result.ReadyNodes++
		}
	}
	sort.Slice(result.Namespaces, func(i, j int) bool { return result.Namespaces[i].Name < result.Namespaces[j].Name })
	return result, http.StatusOK, nil
}

func loadNamespaceOverview(ctx context.Context, dependencies Dependencies, clusterID, namespace string) (namespaceOverviewResponse, int, error) {
	cluster, status, err := findCluster(ctx, dependencies.BCS, clusterID)
	if err != nil {
		return namespaceOverviewResponse{}, status, err
	}
	namespace = strings.TrimSpace(namespace)
	namespaceResult, err := dependencies.Kubernetes.Query(ctx, kubeclient.QueryRequest{
		ClusterID: cluster.ID, Action: "get", Kind: "Namespace", Name: namespace,
	})
	if err != nil {
		return namespaceOverviewResponse{}, http.StatusBadGateway, fmt.Errorf("查询 Namespace %s: %w", namespace, err)
	}
	if len(namespaceResult.Items) != 1 {
		return namespaceOverviewResponse{}, http.StatusBadGateway, fmt.Errorf("查询 Namespace %s: 返回数量异常", namespace)
	}
	query := func(kind string, gvr *kubeclient.ResourceRef) (kubeclient.QueryResult, error) {
		return dependencies.Kubernetes.Query(ctx, kubeclient.QueryRequest{
			ClusterID: cluster.ID, Action: "list", Kind: kind, GVR: gvr, Namespace: namespace, Limit: 100,
		})
	}
	pods, err := query("Pod", nil)
	if err != nil {
		return namespaceOverviewResponse{}, http.StatusBadGateway, fmt.Errorf("查询 Pod: %w", err)
	}
	deployments, err := query("Deployment", nil)
	if err != nil {
		return namespaceOverviewResponse{}, http.StatusBadGateway, fmt.Errorf("查询 Deployment: %w", err)
	}
	events, err := query("Event", &kubeclient.ResourceRef{Version: "v1", Resource: "events"})
	if err != nil {
		return namespaceOverviewResponse{}, http.StatusBadGateway, fmt.Errorf("查询 Event: %w", err)
	}

	result := namespaceOverviewResponse{
		Cluster: cluster, Namespace: namespaceResult.Items[0], Pods: pods.Items, Deployments: deployments.Items,
		WarningEvents: []kubeclient.ResourceSummary{}, PodsHasMore: pods.HasMore,
		DeploymentsHasMore: deployments.HasMore, EventsHasMore: events.HasMore,
	}
	sort.SliceStable(result.Pods, func(i, j int) bool {
		iAbnormal, jAbnormal := podIsAbnormal(result.Pods[i]), podIsAbnormal(result.Pods[j])
		if iAbnormal != jAbnormal {
			return iAbnormal
		}
		return result.Pods[i].Name < result.Pods[j].Name
	})
	sort.Slice(result.Deployments, func(i, j int) bool { return result.Deployments[i].Name < result.Deployments[j].Name })
	for _, event := range events.Items {
		if strings.EqualFold(stringDetail(event, "type"), "Warning") {
			result.WarningEvents = append(result.WarningEvents, event)
		}
	}
	return result, http.StatusOK, nil
}

func podIsAbnormal(pod kubeclient.ResourceSummary) bool {
	if !strings.EqualFold(stringDetail(pod, "phase"), "Running") {
		return true
	}
	ready, readyOK := numberDetail(pod, "ready_containers")
	total, totalOK := numberDetail(pod, "total_containers")
	return readyOK && totalOK && ready < total
}

func stringDetail(resource kubeclient.ResourceSummary, key string) string {
	value, ok := resource.Details[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func numberDetail(resource kubeclient.ResourceSummary, key string) (float64, bool) {
	switch value := resource.Details[key].(type) {
	case int:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case float64:
		return value, true
	default:
		return 0, false
	}
}

func decodeDiagnosisRequest(c *gin.Context) (DiagnosisRequest, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request DiagnosisRequest
	if err := decoder.Decode(&request); err != nil {
		return DiagnosisRequest{}, fmt.Errorf("请求内容不是有效 JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return DiagnosisRequest{}, fmt.Errorf("请求只能包含一个 JSON 对象")
	}
	request.ClusterID = strings.TrimSpace(request.ClusterID)
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.Kind = strings.TrimSpace(request.Kind)
	request.Name = strings.TrimSpace(request.Name)
	if request.ClusterID == "" || request.Namespace == "" || request.Name == "" || request.Kind != "Pod" {
		return DiagnosisRequest{}, fmt.Errorf("必须提供 cluster_id、namespace、kind=Pod 和 name")
	}
	query := kubeclient.QueryRequest{
		ClusterID: request.ClusterID, Namespace: request.Namespace, Kind: request.Kind, Name: request.Name, Action: "get",
	}
	if err := query.NormalizeAndValidate(); err != nil {
		return DiagnosisRequest{}, fmt.Errorf("诊断目标无效: %w", err)
	}
	request.ClusterID, request.Namespace, request.Kind, request.Name = query.ClusterID, query.Namespace, query.Kind, query.Name
	return request, nil
}

func writeUpstreamError(c *gin.Context, message string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.Error(message, "error", err)
	c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "upstream_failed", "message": message}})
}
