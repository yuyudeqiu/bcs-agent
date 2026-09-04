package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

func TestGatewayScaleWithDiscoveryAndPrecondition(t *testing.T) {
	var scaleGets, scaleUpdates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/clusters/BCS-K8S-10001/api/v1":
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[]}`)
		case "/clusters/BCS-K8S-10001/apis":
			fmt.Fprint(w, `{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"apps","preferredVersion":{"groupVersion":"apps/v1","version":"v1"}}]}`)
		case "/clusters/BCS-K8S-10001/apis/apps/v1":
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"apps/v1","resources":[{"name":"statefulsets/scale","kind":"Scale","namespaced":true,"verbs":["get","update"]},{"name":"statefulsets","kind":"StatefulSet","namespaced":true,"verbs":["get","list"]}]}`)
		case "/clusters/BCS-K8S-10001/apis/apps/v1/namespaces/default/statefulsets/db/scale":
			switch r.Method {
			case http.MethodGet:
				scaleGets++
				fmt.Fprint(w, `{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"db","namespace":"default","uid":"uid-1","resourceVersion":"7"},"spec":{"replicas":2},"status":{"replicas":2}}`)
			case http.MethodPut:
				scaleUpdates++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				spec := body["spec"].(map[string]any)
				metadata := body["metadata"].(map[string]any)
				if spec["replicas"] != float64(5) || metadata["resourceVersion"] != "7" {
					t.Fatalf("unsafe scale update: %#v", body)
				}
				fmt.Fprint(w, `{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"db","namespace":"default","uid":"uid-1","resourceVersion":"8"},"spec":{"replicas":5},"status":{"replicas":2}}`)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewGatewayClient(config.BCSConfig{BaseURL: server.URL, APIToken: "token"})
	replicas := int32(5)
	plan, err := client.PrepareScale(context.Background(), ScaleRequest{
		ClusterID: "BCS-K8S-10001", Kind: "statefulset", Namespace: "default", Name: "db", Replicas: &replicas,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != "StatefulSet" || plan.GVR.Resource != "statefulsets" || plan.CurrentReplicas != 2 || plan.TargetReplicas != 5 || plan.ResourceVersion != "7" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	result, err := client.ApplyScale(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Submitted || result.Converged || result.ObservedReplicas != 2 || result.TargetReplicas != 5 || scaleGets != 2 || scaleUpdates != 1 {
		t.Fatalf("unexpected result=%+v gets=%d updates=%d", result, scaleGets, scaleUpdates)
	}
}

func TestGatewayScaleRejectsChangedOrUnsupportedResource(t *testing.T) {
	var changed bool
	var updates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/clusters/BCS-K8S-10001/api/v1":
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[]}`)
		case "/clusters/BCS-K8S-10001/apis":
			fmt.Fprint(w, `{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"apps","preferredVersion":{"groupVersion":"apps/v1","version":"v1"}}]}`)
		case "/clusters/BCS-K8S-10001/apis/apps/v1":
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true,"verbs":["get"]},{"name":"deployments/scale","kind":"Scale","namespaced":true,"verbs":["get","update"]},{"name":"daemonsets","kind":"DaemonSet","namespaced":true,"verbs":["get"]}]}`)
		case "/clusters/BCS-K8S-10001/apis/apps/v1/namespaces/default/deployments/web/scale":
			if r.Method == http.MethodPut {
				updates++
			}
			rv, replicas := "7", 2
			if changed {
				rv, replicas = "8", 3
			}
			fmt.Fprintf(w, `{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"web","namespace":"default","uid":"uid-1","resourceVersion":%q},"spec":{"replicas":%d},"status":{"replicas":%d}}`, rv, replicas, replicas)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewGatewayClient(config.BCSConfig{BaseURL: server.URL, APIToken: "token"})
	replicas := int32(4)
	plan, err := client.PrepareScale(context.Background(), ScaleRequest{ClusterID: "BCS-K8S-10001", Kind: "Deployment", Namespace: "default", Name: "web", Replicas: &replicas})
	if err != nil {
		t.Fatal(err)
	}
	changed = true
	if _, err := client.ApplyScale(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "确认期间已变化") || updates != 0 {
		t.Fatalf("error=%v updates=%d", err, updates)
	}
	if _, err := client.PrepareScale(context.Background(), ScaleRequest{ClusterID: "BCS-K8S-10001", Kind: "DaemonSet", Namespace: "default", Name: "ds", Replicas: &replicas}); err == nil || !strings.Contains(err.Error(), "未提供 scale") {
		t.Fatalf("unsupported error=%v", err)
	}
}

func TestScaleRequestValidation(t *testing.T) {
	zero := int32(0)
	valid := ScaleRequest{ClusterID: "BCS-K8S-10001", Kind: "StatefulSet", Namespace: "default", Name: "db", Replicas: &zero}
	if err := valid.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	for _, request := range []ScaleRequest{
		{},
		{ClusterID: "BCS-K8S-10001", Kind: "StatefulSet", Namespace: "default", Name: "db"},
		{ClusterID: "BCS-K8S-10001", Kind: "StatefulSet", Namespace: "", Name: "db", Replicas: &zero},
		{ClusterID: "BCS-K8S-10001", GVR: &ResourceRef{Group: "apps", Version: "v1", Resource: "statefulsets/scale"}, Namespace: "default", Name: "db", Replicas: &zero},
	} {
		if err := request.NormalizeAndValidate(); err == nil {
			t.Errorf("accepted invalid request: %+v", request)
		}
	}
}
