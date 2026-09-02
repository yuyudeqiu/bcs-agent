package bcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

func TestHTTPClientListProjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bcsapi/v4/bcsproject/v1/projects" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("unexpected authorization: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// 结构与真实 bcsproject 返回一致：code/message/data 包裹 + total/results 分页。
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"total":2,"results":[
			{"projectID":"p1","name":"蓝鲸","projectCode":"blueking","businessID":"2","businessName":"蓝鲸(blueking)","kind":"k8s","useBKRes":false},
			{"projectID":"p2","name":"test","projectCode":"test","businessID":"15","kind":"k8s","useBKRes":true}
		]}}`))
	}))
	defer server.Close()

	client := NewHTTPClient(config.BCSConfig{
		BaseURL:  server.URL,
		APIToken: "test-token",
	})

	projects, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(projects))
	}
	if projects[0].Name != "蓝鲸" || projects[0].ProjectCode != "blueking" {
		t.Errorf("unexpected project[0]: %+v", projects[0])
	}
	if projects[1].UseBKRes != true {
		t.Errorf("unexpected project[1]: %+v", projects[1])
	}
}

func TestHTTPClientListClusters(t *testing.T) {
	// 特殊字符必须作为 projectID 的内容编码，不能变成额外的查询参数。
	const projectID = "project+one&operator=other/name"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/bcsapi/v4/clustermanager/v1/cluster" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		if len(query) != 1 || query.Get("projectID") != projectID {
			t.Errorf("unexpected query: %v", query)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("unexpected authorization: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// clustermanager v1 的 data 为数组，与项目列表的 total/results 结构不同。
		_, _ = w.Write([]byte(`{"code":0,"message":"success","result":true,"data":[
			{"clusterID":"BCS-K8S-10001","clusterName":"测试集群","status":"RUNNING","environment":"prod","clusterBasicSettings":{"version":"v1.23.17","versionName":"display-name"},"kubeConfig":"private-config"},
			{"clusterID":"BCS-K8S-10002","clusterName":"共享集群","status":"INITIALIZATION","is_shared":true},
			{"clusterID":"BCS-K8S-10003","clusterName":"dev-cluster","status":"RUNNING","environment":"","clusterBasicSettings":null}
		]}`))
	}))
	defer server.Close()

	client := NewHTTPClient(config.BCSConfig{BaseURL: server.URL, APIToken: "test-token"})
	got, err := client.ListClusters(context.Background(), projectID)
	if err != nil {
		t.Fatalf("ListClusters() error = %v", err)
	}
	want := []Cluster{
		{ID: "BCS-K8S-10001", Name: "测试集群", Status: "RUNNING", Kubernetes: "v1.23.17", Environment: "prod"},
		{ID: "BCS-K8S-10002", Name: "共享集群", Status: "INITIALIZATION"},
		{ID: "BCS-K8S-10003", Name: "dev-cluster", Status: "RUNNING"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("clusters = %#v, want %#v", got, want)
	}
	output, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, unexpected := range []string{"node_count", "kubeConfig", "private-config"} {
		if strings.Contains(string(output), unexpected) {
			t.Errorf("output contains unexpected field or value %q", unexpected)
		}
	}
	var serialized []map[string]json.RawMessage
	if err := json.Unmarshal(output, &serialized); err != nil {
		t.Fatal(err)
	}
	for _, item := range serialized[1:] {
		for _, field := range []string{"kubernetes_version", "environment"} {
			if _, exists := item[field]; exists {
				t.Errorf("unknown %s should be omitted: %s", field, output)
			}
		}
	}
}

func TestHTTPClientListClustersResponses(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "empty array", status: 200, body: `{"code":0,"data":[]}`},
		{name: "null array", status: 200, body: `{"code":0,"data":null}`},
		{name: "http error", status: 403, body: `forbidden`, wantErr: "HTTP 403"},
		{name: "business error", status: 200, body: `{"code":1001,"message":"permission denied"}`, wantErr: "code=1001"},
		{name: "invalid json", status: 200, body: `not json`, wantErr: "解析响应包裹"},
		{name: "wrong data shape", status: 200, body: `{"code":0,"data":{"results":[]}}`, wantErr: "解析集群列表"},
		{name: "missing data", status: 200, body: `{"code":0}`, wantErr: "解析集群列表"},
		{name: "missing cluster id", status: 200, body: `{"code":0,"data":[{"clusterName":"demo"}]}`, wantErr: "缺少 clusterID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := NewHTTPClient(config.BCSConfig{BaseURL: server.URL})
			clusters, err := client.ListClusters(context.Background(), "p1")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				if clusters != nil {
					t.Fatalf("failed request returned clusters: %#v", clusters)
				}
				return
			}
			if err != nil || clusters == nil || len(clusters) != 0 {
				t.Fatalf("want empty non-nil list, got %#v, error %v", clusters, err)
			}
		})
	}
}

func TestHTTPClientListClustersCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("canceled request should not reach the server")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := NewHTTPClient(config.BCSConfig{BaseURL: server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ListClusters(ctx, "p1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
