package bcs

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

// HTTPClient 通过真实 BCS API 访问集群信息。
type HTTPClient struct {
	baseURL  string
	apiToken string
	client   *http.Client
}

// NewHTTPClient 根据配置构造真实 BCS HTTP 客户端。
// dev 环境证书通常不自带，默认跳过 TLS 校验；生产可设 BCS_INSECURE_SKIP_VERIFY=false 关闭。
func NewHTTPClient(cfg config.BCSConfig) *HTTPClient {
	tlsConfig := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify} // #nosec G402 -- 默认仅面向 dev，生产应关闭

	return &HTTPClient{
		baseURL:  cfg.BaseURL,
		apiToken: cfg.APIToken,
		client: &http.Client{
			Timeout:   15 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}
}

// apiResponse 是 BCS API v4 的统一响应包裹。
type apiResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *HTTPClient) doGet(ctx context.Context, path string) (json.RawMessage, error) {
	url := strings.TrimRight(c.baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("BCS 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var envelope apiResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解析响应包裹: %w (body=%s)", err, string(body))
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("BCS 返回错误 code=%d message=%s", envelope.Code, envelope.Message)
	}
	return envelope.Data, nil
}

// projectListData 是 bcsproject 项目列表接口的 data 载荷：分页对象，results 为项目数组。
type projectListData struct {
	Total   int       `json:"total"`
	Results []Project `json:"results"`
}

// ListProjects 列出当前账号可见的所有项目。
func (c *HTTPClient) ListProjects(ctx context.Context) ([]Project, error) {
	data, err := c.doGet(ctx, "/bcsapi/v4/bcsproject/v1/projects")
	if err != nil {
		return nil, err
	}
	var page projectListData
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("解析项目列表: %w (data=%s)", err, string(data))
	}
	return page.Results, nil
}

// ListClusters 按项目查询集群；接口也可能返回共享集群。
func (c *HTTPClient) ListClusters(ctx context.Context, projectID string) ([]Cluster, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("project_id 不能为空")
	}
	query := url.Values{"projectID": {projectID}}
	// v1 接口直接返回集群数组，当前上游实现不使用 offset/limit。
	data, err := c.doGet(ctx, "/bcsapi/v4/clustermanager/v1/cluster?"+query.Encode())
	if err != nil {
		return nil, err
	}
	// 仅提取列表所需字段，保持工具输出格式，并避免透传 kubeConfig 等敏感数据。
	var items []struct {
		ID            string `json:"clusterID"`
		Name          string `json:"clusterName"`
		Status        string `json:"status"`
		Environment   string `json:"environment"`
		BasicSettings struct {
			Version string `json:"version"`
		} `json:"clusterBasicSettings"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("解析集群列表: %w", err)
	}
	clusters := make([]Cluster, 0, len(items))
	for _, item := range items {
		if item.ID == "" {
			return nil, fmt.Errorf("解析集群列表: 缺少 clusterID")
		}
		clusters = append(clusters, Cluster{
			ID:          item.ID,
			Name:        item.Name,
			Status:      item.Status,
			Kubernetes:  item.BasicSettings.Version,
			Environment: item.Environment,
		})
	}
	return clusters, nil
}

// GetCluster 待接入。
func (c *HTTPClient) GetCluster(_ context.Context, _ string) (ClusterDetail, error) {
	return ClusterDetail{}, fmt.Errorf("BCS 集群详情 API 待接入")
}
