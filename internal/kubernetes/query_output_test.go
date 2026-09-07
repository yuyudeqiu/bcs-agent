package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGatewayFullResourceIsOptIn(t *testing.T) {
	object := map[string]any{
		"apiVersion": "example.io/v1", "kind": "Widget",
		"metadata": map[string]any{"name": "example", "namespace": "default", "annotations": map[string]any{"owner": "demo"}},
		"spec":     map[string]any{"images": []any{"registry.example.com/demo:v1"}},
		"status":   map[string]any{"observedGeneration": float64(2), "syncResults": []any{map[string]any{"image": "registry.example.com/demo:v1", "synced": true}}, "message": strings.Repeat("x", 1500)},
	}
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gateway/clusters/BCS-K8S-10001/apis/example.io/v1":
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"APIResourceList","groupVersion":"example.io/v1","resources":[{"name":"widgets","kind":"Widget","namespaced":true,"verbs":["get","list"]}]}`)
		case "/gateway/clusters/BCS-K8S-10001/apis/example.io/v1/namespaces/default/widgets/example":
			if err := json.NewEncoder(w).Encode(object); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	// 同一个客户端先读摘要、再 full、再省略 output；默认值不会受上次调用影响。
	for _, output := range []string{"", "summary", "full", ""} {
		got, err := client.Query(context.Background(), QueryRequest{
			ClusterID: "BCS-K8S-10001", Action: "get", Name: "example", Namespace: "default", Output: output,
			GVR: &ResourceRef{Group: "example.io", Version: "v1", Resource: "widgets"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Count != 1 || got.Source != "kubernetes" {
			t.Fatalf("unexpected result: %+v", got)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if output == "full" {
			if got.Output != "full" || got.Items[0].Details != nil || got.Items[0].Redacted {
				t.Fatalf("unexpected full result: %s", encoded)
			}
			// 转回通用 JSON，避免动态客户端的 int64 和 fixture 的 float64 差异。
			var decoded QueryResult
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Items[0].Resource, object) {
				t.Fatalf("full resource changed: %s", encoded)
			}
		} else if got.Output != "summary" || got.Items[0].Resource != nil || strings.Contains(string(encoded), "syncResults") || strings.Contains(string(encoded), "registry.example.com") {
			t.Fatalf("summary leaked full content: %s", encoded)
		}
	}
	object["spec"] = map[string]any{"payload": strings.Repeat("x", maxFullResourceBytes)}
	got, err := client.Query(context.Background(), QueryRequest{ClusterID: "BCS-K8S-10001", Action: "get", Name: "example", Namespace: "default", Output: "full", GVR: &ResourceRef{Group: "example.io", Version: "v1", Resource: "widgets"}})
	if err == nil || !strings.Contains(err.Error(), "64 KiB") || got.Items != nil || got.Source != "" {
		t.Fatalf("oversized resource produced partial output: %+v %v", got, err)
	}
}

func TestFullOutputRedactionPreservesStructure(t *testing.T) {
	var object map[string]any
	err := json.Unmarshal([]byte(`{
  "apiVersion":"example.io/v1","kind":"Widget","metadata":{"name":"example","namespace":"default","annotations":{"kubectl.kubernetes.io/last-applied-configuration":"embedded-credential","description":"test-token reflected"}},
  "spec":{"images":["registry.example.com/demo:v1"],"password":"private-password","nested":{"api_key":"private-key"},"env":[{"name":"DB_PASSWORD","value":"env-password"},{"name":"MODE","value":"normal"}],"values":["test-token"]},
  "status":{"syncResults":[{"image":"demo:v1","synced":true}]}
 }`), &object)
	if err != nil {
		t.Fatal(err)
	}
	item := unstructured.Unstructured{Object: object}
	before, _ := json.Marshal(object)
	got, err := formatQueryResource(item, "full", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	if !got.Redacted {
		t.Fatal("missing redaction marker")
	}
	for _, value := range []string{"embedded-credential", "test-token", "private-password", "private-key", "env-password"} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("credential leaked: %s", value)
		}
	}
	for _, value := range []string{"registry.example.com/demo:v1", "syncResults", "normal", "[REDACTED]"} {
		if !strings.Contains(string(encoded), value) {
			t.Errorf("missing preserved value: %s", value)
		}
	}
	after, _ := json.Marshal(object)
	if string(before) != string(after) {
		t.Fatal("redaction mutated API response")
	}
}

func TestFullOutputValidationBeforeAPI(t *testing.T) {
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached API") })
	for _, q := range []QueryRequest{
		{Action: "list", Kind: "Node", Output: "full"},
		{Action: "get", Kind: "Node", Output: "full"},
		{Action: "get", Kind: "Node", Name: "worker", Output: "raw"},
		{Action: "get", Kind: "Node", Name: "worker", Output: "FULL"},
		{Action: "get", Name: "secret", Namespace: "default", Output: "full", GVR: &ResourceRef{Version: "v1", Resource: "secrets"}},
	} {
		q.ClusterID = "BCS-K8S-10001"
		if _, err := client.Query(context.Background(), q); err == nil {
			t.Errorf("accepted: %+v", q)
		}
	}
}

func TestMockFullOutput(t *testing.T) {
	client := NewMockClient()
	for _, output := range []string{"full", ""} {
		got, err := client.Query(context.Background(), QueryRequest{ClusterID: "BCS-K8S-40890", Action: "get", Kind: "Widget", Namespace: "default", Name: "example", Output: output})
		if err != nil {
			t.Fatal(err)
		}
		if got.Source != "mock" || got.Count != 1 {
			t.Fatalf("invalid mock result: %+v", got)
		}
		if output == "full" && (got.Items[0].Resource["spec"] == nil || got.Items[0].Resource["status"] == nil) {
			t.Fatal("mock missing full spec/status")
		}
		if output == "" && (got.Output != "summary" || got.Items[0].Resource != nil) {
			t.Fatal("mock default changed after full")
		}
	}
}
