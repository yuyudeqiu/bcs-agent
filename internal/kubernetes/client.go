package kubernetes

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

type Client interface {
	Query(ctx context.Context, request QueryRequest) (QueryResult, error)
	Logs(ctx context.Context, request LogsRequest) (LogsResult, error)
	PrepareScale(ctx context.Context, request ScaleRequest) (ScalePlan, error)
	ApplyScale(ctx context.Context, plan ScalePlan) (ScaleResult, error)
}

// GatewayClient 通过 BCS /clusters/{clusterID} 代理访问 Kubernetes 原生 API。
type GatewayClient struct {
	cfg            config.BCSConfig
	discoveryMu    sync.RWMutex
	discoveryCache map[string]discoveryCacheEntry
}

func NewGatewayClient(cfg config.BCSConfig) *GatewayClient {
	return &GatewayClient{cfg: cfg, discoveryCache: make(map[string]discoveryCacheEntry)}
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
