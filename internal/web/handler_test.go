package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuyudeqiu/bcs-agent/internal/bcs"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

type fakeClient struct {
	projectID string
	clusters  []bcs.Cluster
	err       error
}

type recordingKubernetesClient struct {
	*kubeclient.MockClient
	requests []kubeclient.QueryRequest
}

func (c *recordingKubernetesClient) Query(ctx context.Context, request kubeclient.QueryRequest) (kubeclient.QueryResult, error) {
	c.requests = append(c.requests, request)
	return c.MockClient.Query(ctx, request)
}

func (c *fakeClient) ListProjects(context.Context) ([]bcs.Project, error) {
	return nil, nil
}

func (c *fakeClient) ListClusters(_ context.Context, projectID string) ([]bcs.Cluster, error) {
	c.projectID = projectID
	return c.clusters, c.err
}

func (c *fakeClient) GetCluster(context.Context, string) (bcs.ClusterDetail, error) {
	return bcs.ClusterDetail{}, nil
}

func TestListClusters(t *testing.T) {
	client := &fakeClient{clusters: []bcs.Cluster{{ID: "BCS-K8S-10001", Name: "demo", Status: "RUNNING"}}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters?project_id=%20p1%20", nil)

	NewHandler(Dependencies{BCS: client}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if client.projectID != "p1" {
		t.Fatalf("project ID = %q", client.projectID)
	}
	var response struct {
		Data  []bcs.Cluster `json:"data"`
		Total int           `json:"total"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].ID != "BCS-K8S-10001" {
		t.Fatalf("response = %#v", response)
	}
}

func TestListClustersFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	NewHandler(Dependencies{BCS: &fakeClient{err: errors.New("upstream unavailable")}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "upstream unavailable") {
		t.Fatalf("response leaks internal error: %s", recorder.Body.String())
	}
}

func TestIndexAndHealth(t *testing.T) {
	handler := NewHandler(Dependencies{BCS: &fakeClient{}})

	for _, test := range []struct {
		path        string
		contentType string
		body        string
	}{
		{path: "/", contentType: "text/html", body: "集群运维工作台"},
		{path: "/clusters/BCS-K8S-40888", contentType: "text/html", body: "选择 Namespace 查看工作负载和 Pod"},
		{path: "/clusters/BCS-K8S-40888/namespaces/default", contentType: "text/html", body: "异常 Pod 优先展示"},
		{path: "/healthz", contentType: "application/json", body: `"status":"ok"`},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Type"), test.contentType) || !strings.Contains(recorder.Body.String(), test.body) {
			t.Errorf("GET %s: status=%d content-type=%q body=%q", test.path, recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
		}
	}
}

func TestListNamespaces(t *testing.T) {
	kubernetesClient := &recordingKubernetesClient{MockClient: kubeclient.NewMockClient()}
	handler := NewHandler(Dependencies{BCS: bcs.NewMockClient(), Kubernetes: kubernetesClient})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/BCS-K8S-40890/namespaces", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response namespaceListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Cluster.ID != "BCS-K8S-40890" || response.NodeCount != 3 || response.ReadyNodes != 2 {
		t.Fatalf("namespace list cluster or nodes = %#v", response)
	}
	if len(response.Namespaces) != 2 || response.Namespaces[0].Name != "default" || response.Namespaces[1].Name != "production" {
		t.Fatalf("namespaces = %#v", response.Namespaces)
	}
	if len(kubernetesClient.requests) != 2 || kubernetesClient.requests[0].Kind != "Node" || kubernetesClient.requests[1].Kind != "Namespace" {
		t.Fatalf("query requests = %#v", kubernetesClient.requests)
	}
}

func TestListNamespacesContinuation(t *testing.T) {
	kubernetesClient := &recordingKubernetesClient{MockClient: kubeclient.NewMockClient()}
	handler := NewHandler(Dependencies{BCS: bcs.NewMockClient(), Kubernetes: kubernetesClient})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/BCS-K8S-40890/namespaces?continue=1", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response namespaceListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Namespaces) != 1 || response.Namespaces[0].Name != "production" || response.HasMore {
		t.Fatalf("continued namespaces = %#v", response)
	}
	if len(kubernetesClient.requests) != 2 || kubernetesClient.requests[1].Continue != "1" {
		t.Fatalf("query requests = %#v", kubernetesClient.requests)
	}
}

func TestNamespaceOverview(t *testing.T) {
	kubernetesClient := &recordingKubernetesClient{MockClient: kubeclient.NewMockClient()}
	handler := NewHandler(Dependencies{BCS: bcs.NewMockClient(), Kubernetes: kubernetesClient})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/BCS-K8S-40890/namespaces/production/overview", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response namespaceOverviewResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Cluster.ID != "BCS-K8S-40890" || response.Namespace.Name != "production" {
		t.Fatalf("overview target = %#v", response)
	}
	if len(response.Pods) != 1 || response.Pods[0].Name != "web-1" || !podIsAbnormal(response.Pods[0]) {
		t.Fatalf("pods = %#v", response.Pods)
	}
	if len(response.Deployments) != 1 || response.Deployments[0].Name != "web" {
		t.Fatalf("deployments = %#v", response.Deployments)
	}
	if len(response.WarningEvents) != 1 || response.WarningEvents[0].Details["reason"] != "FailedScheduling" {
		t.Fatalf("warning events = %#v", response.WarningEvents)
	}
	if len(kubernetesClient.requests) != 4 {
		t.Fatalf("query requests = %#v", kubernetesClient.requests)
	}
	eventRequest := kubernetesClient.requests[3]
	if eventRequest.Kind != "Event" || eventRequest.GVR == nil || eventRequest.GVR.Group != "" || eventRequest.GVR.Version != "v1" || eventRequest.GVR.Resource != "events" {
		t.Fatalf("Event query did not use core/v1/events: %#v", eventRequest)
	}
}

func TestNamespacesClusterNotFound(t *testing.T) {
	handler := NewHandler(Dependencies{BCS: bcs.NewMockClient(), Kubernetes: kubeclient.NewMockClient()})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/not-found/namespaces", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestDiagnosePod(t *testing.T) {
	var got DiagnosisRequest
	diagnose := func(_ context.Context, request DiagnosisRequest, emit func(DiagnosisEvent) error) error {
		got = request
		if err := emit(DiagnosisEvent{Type: "tool_call", ToolName: "kubernetes_query"}); err != nil {
			return err
		}
		return emit(DiagnosisEvent{Type: "text_delta", Content: "Pod 因 CPU 不足无法调度。"})
	}
	handler := NewHandler(Dependencies{BCS: bcs.NewMockClient(), Kubernetes: kubeclient.NewMockClient(), Diagnose: diagnose})
	recorder := httptest.NewRecorder()
	body := `{"cluster_id":"BCS-K8S-40890","namespace":"production","kind":"Pod","name":"web-1"}`
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/diagnoses", strings.NewReader(body)))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("status = %d, content-type = %q, body = %s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	if got.ClusterID != "BCS-K8S-40890" || got.Namespace != "production" || got.Name != "web-1" {
		t.Fatalf("diagnosis request = %+v", got)
	}
	if !strings.Contains(recorder.Body.String(), `"type":"text_delta"`) || !strings.Contains(recorder.Body.String(), `"type":"done"`) {
		t.Fatalf("stream = %s", recorder.Body.String())
	}
}

func TestDiagnoseUnavailableAndInvalid(t *testing.T) {
	withoutAgent := NewHandler(Dependencies{BCS: bcs.NewMockClient(), Kubernetes: kubeclient.NewMockClient()})
	recorder := httptest.NewRecorder()
	withoutAgent.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/diagnoses", strings.NewReader(`{}`)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d", recorder.Code)
	}

	withAgent := NewHandler(Dependencies{
		BCS: bcs.NewMockClient(), Kubernetes: kubeclient.NewMockClient(),
		Diagnose: func(context.Context, DiagnosisRequest, func(DiagnosisEvent) error) error { return nil },
	})
	recorder = httptest.NewRecorder()
	withAgent.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/diagnoses", strings.NewReader(`{"cluster_id":"x","namespace":"default","kind":"Deployment","name":"web"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
