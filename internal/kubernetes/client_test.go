package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

func TestGatewayNodeSummary(t *testing.T) {
	pages := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected method or headers: %s %v", r.Method, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/clusters/BCS-K8S-10001/version":
			_, _ = w.Write([]byte(`{"gitVersion":"v1.23.17"}`))
		case "/clusters/BCS-K8S-10001/api/v1/nodes":
			pages++
			if r.URL.Query().Get("limit") != "500" {
				t.Errorf("unexpected limit: %s", r.URL.RawQuery)
			}
			switch r.URL.Query().Get("continue") {
			case "":
				_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NodeList","metadata":{"continue":"next+/="},"items":[
					{"metadata":{"name":"control-plane","labels":{"node-role.kubernetes.io/control-plane":""}},"spec":{"unschedulable":true},"status":{"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"True"}]}},
					{"metadata":{"name":"worker-1"},"status":{"conditions":[{"type":"Ready","status":"False"}]}}
				]}`))
			case "next+/=":
				_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NodeList","metadata":{},"items":[
					{"metadata":{"name":"worker-2"},"status":{"conditions":[{"type":"Ready","status":"Unknown"}]}},
					{"metadata":{"name":"worker-3"},"status":{"conditions":[]}}
				]}`))
			default:
				t.Errorf("unexpected continue token: %s", r.URL.RawQuery)
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewGatewayClient(config.BCSConfig{BaseURL: server.URL + "/", APIToken: "test-token", InsecureSkipVerify: true})

	// 验证示例 /version 和节点 API 都保留集群网关前缀。
	core, err := client.coreClient("BCS-K8S-10001")
	if err != nil {
		t.Fatal(err)
	}
	version, err := core.RESTClient().Get().AbsPath("/version").DoRaw(context.Background())
	if err != nil || string(version) != `{"gitVersion":"v1.23.17"}` {
		t.Fatalf("version = %s, error = %v", version, err)
	}
	got, err := client.GetNodeSummary(context.Background(), "BCS-K8S-10001")
	want := NodeSummary{ClusterID: "BCS-K8S-10001", TotalNodes: 4, ReadyNodes: 1, NotReadyNodes: 3, Source: "kubernetes"}
	if err != nil || got != want || pages != 2 {
		t.Fatalf("summary = %#v, error = %v, pages = %d; want %#v and 2 pages", got, err, pages, want)
	}
}

func TestGatewaySummaryResponses(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{"empty", 200, `{"apiVersion":"v1","kind":"NodeList","items":[]}`, false},
		{"forbidden", 403, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","code":403,"message":"nodes is forbidden"}`, true},
		{"invalid json", 200, `invalid`, true},
		{"repeated page", 200, `{"apiVersion":"v1","kind":"NodeList","metadata":{"continue":"same"},"items":[]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := NewGatewayClient(config.BCSConfig{BaseURL: server.URL, APIToken: "test-token", InsecureSkipVerify: true})
			got, err := client.GetNodeSummary(context.Background(), "BCS-K8S-10001")
			if (err != nil) != test.wantError {
				t.Fatalf("summary = %#v, error = %v", got, err)
			}
			if test.wantError && got != (NodeSummary{}) {
				t.Fatalf("failed query returned partial summary: %#v", got)
			}
			if test.name == "forbidden" && !apierrors.IsForbidden(err) {
				t.Fatalf("expected wrapped Kubernetes forbidden error, got %v", err)
			}
			if !test.wantError && got != (NodeSummary{ClusterID: "BCS-K8S-10001", Source: "kubernetes"}) {
				t.Fatalf("unexpected empty summary: %#v", got)
			}
		})
	}
}

func TestGatewayPartialFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continue") == "" {
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NodeList","metadata":{"continue":"next"},"items":[{"metadata":{"name":"node-1"}}]}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","code":403}`))
	}))
	defer server.Close()
	client := NewGatewayClient(config.BCSConfig{BaseURL: server.URL, APIToken: "test-token", InsecureSkipVerify: true})
	got, err := client.GetNodeSummary(context.Background(), "BCS-K8S-10001")
	if !apierrors.IsForbidden(err) || got != (NodeSummary{}) {
		t.Fatalf("partial failure must not report success: %#v, %v", got, err)
	}
}

func TestGatewayInputAndCancellation(t *testing.T) {
	client := NewGatewayClient(config.BCSConfig{BaseURL: "https://bcs.example.com", APIToken: "test-token"})
	for _, id := range []string{"", " ", "..", "../other", "a/b", "a?x=y", "a%2Fb"} {
		if _, err := client.coreClient(id); err == nil {
			t.Errorf("invalid cluster ID %q accepted", id)
		}
	}
	for _, cfg := range []config.BCSConfig{
		{BaseURL: "https://bcs.example.com"},
		{BaseURL: "", APIToken: "test-token"},
		{BaseURL: "https://bcs.example.com?token=x", APIToken: "test-token"},
	} {
		if _, err := NewGatewayClient(cfg).coreClient("BCS-K8S-10001"); err == nil {
			t.Error("invalid gateway config accepted")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("canceled request reached server")
		fmt.Fprint(w, "{}")
	}))
	defer server.Close()
	client = NewGatewayClient(config.BCSConfig{BaseURL: server.URL, APIToken: "test-token"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetNodeSummary(ctx, "BCS-K8S-10001"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
}
