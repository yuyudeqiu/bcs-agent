package kubernetes

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

type NodeSummary struct {
	ClusterID     string `json:"cluster_id"`
	TotalNodes    int    `json:"total_nodes"`
	ReadyNodes    int    `json:"ready_nodes"`
	NotReadyNodes int    `json:"not_ready_nodes"`
	Source        string `json:"source"`
}

type Client interface {
	GetNodeSummary(ctx context.Context, clusterID string) (NodeSummary, error)
	Query(ctx context.Context, request QueryRequest) (QueryResult, error)
}

// GatewayClient 通过 BCS /clusters/{clusterID} 代理访问 Kubernetes 原生 API。
type GatewayClient struct {
	cfg config.BCSConfig
}

func NewGatewayClient(cfg config.BCSConfig) *GatewayClient {
	return &GatewayClient{cfg: cfg}
}

var clusterIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func validateClusterID(clusterID string) error {
	if !clusterIDPattern.MatchString(clusterID) {
		return fmt.Errorf("cluster_id 不能为空，且只能包含字母、数字、下划线和连字符")
	}
	return nil
}

func (c *GatewayClient) coreClient(clusterID string) (*coreclient.CoreV1Client, error) {
	cfg, err := c.restConfig(clusterID)
	if err != nil {
		return nil, err
	}
	client, err := coreclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 Kubernetes 客户端: %w", err)
	}
	return client, nil
}

func (c *GatewayClient) restConfig(clusterID string) (*rest.Config, error) {
	if err := validateClusterID(clusterID); err != nil {
		return nil, err
	}
	base, err := url.Parse(c.cfg.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("BCS_BASE_URL 必须是有效的 HTTP(S) 地址，不能包含凭证、查询参数或片段")
	}
	if strings.TrimSpace(c.cfg.APIToken) == "" {
		return nil, fmt.Errorf("缺少 BCS_API_TOKEN")
	}
	return &rest.Config{
		Host:        base.JoinPath("clusters", clusterID).String(),
		BearerToken: c.cfg.APIToken,
		Timeout:     15 * time.Second,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: c.cfg.InsecureSkipVerify,
		},
		ContentConfig: rest.ContentConfig{
			AcceptContentTypes: "application/json",
			ContentType:        "application/json",
		},
	}, nil
}

func (c *GatewayClient) GetNodeSummary(ctx context.Context, clusterID string) (NodeSummary, error) {
	client, err := c.coreClient(clusterID)
	if err != nil {
		return NodeSummary{}, err
	}
	// 限制整个分页查询的耗时，而不只是单个 HTTP 请求。
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	summary := NodeSummary{ClusterID: clusterID, Source: "kubernetes"}
	options := metav1.ListOptions{Limit: 500}
	seenTokens := make(map[string]bool)
	for {
		page, err := client.Nodes().List(ctx, options)
		if err != nil {
			return NodeSummary{}, fmt.Errorf("查询集群 %s 的节点: %w", clusterID, err)
		}
		for _, node := range page.Items {
			summary.TotalNodes++
			ready := false
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady {
					ready = condition.Status == corev1.ConditionTrue
					break
				}
			}
			if ready {
				summary.ReadyNodes++
			} else {
				// False、Unknown 或缺失 Ready 条件都不能计为就绪。
				summary.NotReadyNodes++
			}
		}
		if page.Continue == "" {
			return summary, nil
		}
		if seenTokens[page.Continue] {
			return NodeSummary{}, fmt.Errorf("查询节点分页返回重复的 continue 标记，未获取完整列表")
		}
		seenTokens[page.Continue] = true
		options.Continue = page.Continue
	}
}
