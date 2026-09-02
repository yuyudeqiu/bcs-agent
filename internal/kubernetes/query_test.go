package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const coreDiscovery = `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true,"verbs":["list","get"]},{"name":"namespaces","kind":"Namespace","namespaced":false,"verbs":["list","get"]},{"name":"nodes","kind":"Node","namespaced":false,"verbs":["list","get"]},{"name":"events","kind":"Event","namespaced":true,"verbs":["list","get"]}]}`
const appsDiscovery = `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true,"verbs":["list","get"]}]}`

func queryTestClient(t *testing.T, handler http.HandlerFunc) *GatewayClient {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected method/auth")
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return NewGatewayClient(config.BCSConfig{BaseURL: server.URL + "/gateway", APIToken: "test-token", InsecureSkipVerify: true})
}

func serveDiscovery(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/gateway/clusters/BCS-K8S-10001/api/v1":
		fmt.Fprint(w, coreDiscovery)
		return true
	case "/gateway/clusters/BCS-K8S-10001/apis/apps/v1":
		fmt.Fprint(w, appsDiscovery)
		return true
	case "/gateway/clusters/BCS-K8S-10001/apis":
		fmt.Fprint(w, `{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}}]}`)
		return true
	}
	return false
}

func TestQueryResourceRoutesAndSummaries(t *testing.T) {
	for _, test := range []struct {
		kind, namespace, path, body, key string
		value                            any
	}{
		{"Pod", "default", "/api/v1/namespaces/default/pods", `{"spec":{"containers":[{"name":"web","env":[{"name":"PASSWORD","value":"private-value"}]}]},"status":{"phase":"Running","containerStatuses":[{"name":"web","ready":false,"restartCount":3,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}`, "restart_count", float64(3)},
		{"Namespace", "", "/api/v1/namespaces", `{"status":{"phase":"Active"}}`, "phase", "Active"},
		{"Node", "", "/api/v1/nodes", `{"status":{"conditions":[{"type":"Ready","status":"True"}],"nodeInfo":{"kubeletVersion":"v1.28.0"}}}`, "ready", "True"},
		{"Deployment", "default", "/apis/apps/v1/namespaces/default/deployments", `{"spec":{"replicas":3},"status":{"readyReplicas":2,"availableReplicas":2}}`, "desired_replicas", float64(3)},
		{"Event", "default", "/api/v1/namespaces/default/events", `{"reason":"FailedScheduling","message":"test-token reflected","type":"Warning","involvedObject":{"kind":"Pod","name":"web"}}`, "message", "[REDACTED] reflected"},
	} {
		for _, action := range []string{"list", "get"} {
			t.Run(test.kind+"/"+action, func(t *testing.T) {
				calls := 0
				client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
					if serveDiscovery(w, r) {
						return
					}
					calls++
					want := "/gateway/clusters/BCS-K8S-10001" + test.path
					if action == "get" {
						want += "/example"
					}
					if r.URL.Path != want {
						t.Errorf("path=%s, want %s", r.URL.Path, want)
					}
					var object map[string]any
					if err := json.Unmarshal([]byte(test.body), &object); err != nil {
						t.Fatal(err)
					}
					object["metadata"] = map[string]any{"name": "example", "namespace": test.namespace, "annotations": map[string]any{"private": "private-value"}}
					gv := queryResources[test.kind].groupVersion.String()
					if action == "list" {
						if r.URL.Query().Get("limit") != "50" {
							t.Errorf("missing default limit")
						}
						// 列表条目不携带 TypeMeta，与真实 Kubernetes 响应一致。
						json.NewEncoder(w).Encode(map[string]any{"kind": test.kind + "List", "apiVersion": gv, "items": []any{object}})
					} else {
						object["kind"] = test.kind
						object["apiVersion"] = gv
						json.NewEncoder(w).Encode(object)
					}
				})
				q := QueryRequest{ClusterID: "BCS-K8S-10001", Action: action, Kind: test.kind, Namespace: test.namespace}
				if action == "get" {
					q.Name = "example"
				}
				got, err := client.Query(context.Background(), q)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(got)
				if strings.Contains(string(data), "private-value") || strings.Contains(string(data), "test-token") {
					t.Fatalf("sensitive field leaked: %s", data)
				}
				var result QueryResult
				json.Unmarshal(data, &result)
				if calls != 1 || result.Count != 1 || result.Source != "kubernetes" || result.HasMore || result.Items[0].Details[test.key] != test.value {
					t.Fatalf("unexpected result: %s", data)
				}
			})
		}
	}
}

func TestQueryPaginationAcrossNamespaces(t *testing.T) {
	calls := 0
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if serveDiscovery(w, r) {
			return
		}
		calls++
		if r.URL.Path != "/gateway/clusters/BCS-K8S-10001/api/v1/pods" || r.URL.Query().Get("limit") != "1" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		switch r.URL.Query().Get("continue") {
		case "":
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"PodList","metadata":{"continue":"next+/="},"items":[{"metadata":{"name":"web","namespace":"default"}}]}`)
		case "next+/=":
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"PodList","items":[{"metadata":{"name":"web","namespace":"production"}}]}`)
		default:
			t.Errorf("incorrect continuation")
		}
	})
	q := QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "Pod", AllNamespaces: true, Limit: 1}
	first, err := client.Query(context.Background(), q)
	if err != nil || !first.HasMore || first.Continue != "next+/=" || first.Count != 1 {
		t.Fatalf("first=%+v, err=%v", first, err)
	}
	q.Continue = first.Continue
	last, err := client.Query(context.Background(), q)
	if err != nil || last.HasMore || last.Count != 1 || calls != 2 || last.Items[0].Namespace != "production" {
		t.Fatalf("last=%+v, err=%v", last, err)
	}
}

func TestQueryValidationBeforeRequest(t *testing.T) {
	requests := 0
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) { requests++ })
	valid := QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "Pod", Namespace: "default"}
	for _, change := range []func(*QueryRequest){
		func(q *QueryRequest) { q.ClusterID = "../bad" }, func(q *QueryRequest) { q.Action = "delete" },
		func(q *QueryRequest) { q.Kind = "pods/log" }, func(q *QueryRequest) { q.Namespace = "../bad" },
		func(q *QueryRequest) { q.AllNamespaces = true },
		func(q *QueryRequest) { q.Action = "get" }, func(q *QueryRequest) { q.Name = "web" },
		func(q *QueryRequest) { q.Limit = -1 }, func(q *QueryRequest) { q.Limit = 101 },
		func(q *QueryRequest) { q.Action = "get"; q.Name = "../web" },
		func(q *QueryRequest) { q.Action = "get"; q.Name = "web"; q.Limit = 1 },
		func(q *QueryRequest) { q.Action = "get"; q.Name = "web"; q.Continue = "next" },
		func(q *QueryRequest) { q.Action = "get"; q.Name = "web"; q.Namespace = ""; q.AllNamespaces = true },
	} {
		q := valid
		change(&q)
		if _, err := client.Query(context.Background(), q); err == nil {
			t.Errorf("accepted %+v", q)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid requests reached server: %d", requests)
	}
}

func TestQueryFailureAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		discovery bool
	}{
		{"forbidden", 403, `{"kind":"Status","apiVersion":"v1","reason":"Forbidden","code":403,"message":"test-token private-value"}`, false},
		{"not found", 404, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`, false},
		{"expired page", 410, `{"kind":"Status","apiVersion":"v1","reason":"Expired","code":410}`, false},
		{"bad JSON", 200, `invalid test-token`, false},
		{"missing identity", 200, `{"kind":"PodList","apiVersion":"v1","items":[{}]}`, false},
		{"wrong list", 200, `{"kind":"SecretList","apiVersion":"v1","items":[]}`, false},
		{"wrong namespace", 200, `{"kind":"PodList","apiVersion":"v1","items":[{"metadata":{"name":"web","namespace":"other"}}]}`, false},
		{"repeated page", 200, `{"kind":"PodList","apiVersion":"v1","metadata":{"continue":"same"},"items":[]}`, false},
		{"discovery denied", 403, `{"kind":"Status","apiVersion":"v1","reason":"Forbidden","code":403}`, true},
		{"unsupported resource", 200, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if !test.discovery && serveDiscovery(w, r) {
					return
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			})
			got, err := client.Query(context.Background(), QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "Pod", Namespace: "default", Continue: "same"})
			if err == nil || got.Source != "" || got.Items != nil {
				t.Fatalf("failure returned success: %+v, %v", got, err)
			}
			if strings.Contains(err.Error(), "test-token") || strings.Contains(err.Error(), "private-value") {
				t.Fatalf("error leaked body: %v", err)
			}
			if test.status == 403 && !apierrors.IsForbidden(err) {
				t.Fatalf("lost error chain: %v", err)
			}
			if test.status == 404 && !apierrors.IsNotFound(err) {
				t.Fatalf("lost error chain: %v", err)
			}
		})
	}
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled request reached server") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Query(ctx, QueryRequest{ClusterID: "BCS-K8S-10001", Kind: "Node", Action: "list"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
}

func TestQueryEmptyListAndMock(t *testing.T) {
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if serveDiscovery(w, r) {
			return
		}
		fmt.Fprint(w, `{"kind":"NodeList","apiVersion":"v1","items":[]}`)
	})
	got, err := client.Query(context.Background(), QueryRequest{ClusterID: "BCS-K8S-10001", Kind: "Node", Action: "list"})
	if err != nil || got.Count != 0 || got.HasMore || got.Items == nil {
		t.Fatalf("empty: %+v %v", got, err)
	}
	mock := NewMockClient()
	for _, kind := range []string{"Pod", "Deployment", "Namespace", "Node", "Event"} {
		q := QueryRequest{ClusterID: "BCS-K8S-40890", Kind: kind, Action: "list", Limit: 1, AllNamespaces: queryResources[kind].namespaced}
		got, err := mock.Query(context.Background(), q)
		if err != nil || got.Source != "mock" || got.Count != 1 {
			t.Fatalf("%s: %+v %v", kind, got, err)
		}
		if got.HasMore {
			q.Continue = got.Continue
			next, err := mock.Query(context.Background(), q)
			if err != nil || next.Count != 1 {
				t.Fatalf("mock pagination: %+v %v", next, err)
			}
		}
		q.Action = "get"
		q.Limit = 0
		q.Continue = ""
		q.AllNamespaces = false
		q.Name = got.Items[0].Name
		q.Namespace = got.Items[0].Namespace
		detail, err := mock.Query(context.Background(), q)
		if err != nil || detail.Count != 1 {
			t.Fatalf("mock get: %+v %v", detail, err)
		}
		q.Name = "missing"
		if _, err := mock.Query(context.Background(), q); !apierrors.IsNotFound(err) {
			t.Fatalf("expected not found, got %v", err)
		}
	}
}

func TestQueryDiscoveryCRDVersionsAmbiguityAndCache(t *testing.T) {
	discoveryCalls := 0
	resourceCalls := 0
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gateway/clusters/BCS-K8S-10001/api/v1":
			discoveryCalls++
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true,"verbs":["list","get"]},{"name":"secrets","kind":"Secret","namespaced":true,"verbs":["list","get"]}]}`)
		case "/gateway/clusters/BCS-K8S-10001/apis":
			discoveryCalls++
			fmt.Fprint(w, `{"kind":"APIGroupList","apiVersion":"v1","groups":[
				{"name":"batch","versions":[{"groupVersion":"batch/v1beta1","version":"v1beta1"}],"preferredVersion":{"groupVersion":"batch/v1beta1","version":"v1beta1"}},
				{"name":"example.io","versions":[{"groupVersion":"example.io/v1","version":"v1"},{"groupVersion":"example.io/v1beta1","version":"v1beta1"}],"preferredVersion":{"groupVersion":"example.io/v1","version":"v1"}},
				{"name":"other.io","versions":[{"groupVersion":"other.io/v1","version":"v1"}],"preferredVersion":{"groupVersion":"other.io/v1","version":"v1"}}
			]}`)
		case "/gateway/clusters/BCS-K8S-10001/apis/batch/v1beta1":
			discoveryCalls++
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"batch/v1beta1","resources":[{"name":"cronjobs","kind":"CronJob","namespaced":true,"verbs":["list","get"]}]}`)
		case "/gateway/clusters/BCS-K8S-10001/apis/example.io/v1":
			discoveryCalls++
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"example.io/v1","resources":[{"name":"widgets","kind":"Widget","namespaced":true,"verbs":["list","get"]}]}`)
		case "/gateway/clusters/BCS-K8S-10001/apis/other.io/v1":
			discoveryCalls++
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"other.io/v1","resources":[{"name":"widgets","kind":"Widget","namespaced":true,"verbs":["list","get"]}]}`)
		case "/gateway/clusters/BCS-K8S-10001/apis/example.io/v1beta1":
			discoveryCalls++
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"example.io/v1beta1","resources":[{"name":"widgets","kind":"Widget","namespaced":true,"verbs":["list","get"]}]}`)
		case "/gateway/clusters/BCS-K8S-10001/apis/batch/v1beta1/namespaces/default/cronjobs":
			resourceCalls++
			fmt.Fprint(w, `{"apiVersion":"batch/v1beta1","kind":"CronJobList","items":[{"metadata":{"name":"cleanup","namespace":"default"}}]}`)
		case "/gateway/clusters/BCS-K8S-10001/apis/example.io/v1beta1/namespaces/default/widgets/example":
			resourceCalls++
			fmt.Fprint(w, `{"apiVersion":"example.io/v1beta1","kind":"Widget","metadata":{"name":"example","namespace":"default"},"status":{"phase":"Ready","conditions":[{"type":"Available","status":"True","message":"working"}]}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	query := QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "CronJob", Namespace: "default"}
	for i := 0; i < 2; i++ {
		got, err := client.Query(context.Background(), query)
		if err != nil || got.GVR != (ResourceRef{Group: "batch", Version: "v1beta1", Resource: "cronjobs"}) || got.Count != 1 {
			t.Fatalf("CronJob query = %+v, %v", got, err)
		}
	}
	if discoveryCalls != 5 || resourceCalls != 2 {
		t.Fatalf("discovery cache not reused: discovery=%d resource=%d", discoveryCalls, resourceCalls)
	}

	_, err := client.Query(context.Background(), QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "Widget", Namespace: "default"})
	if err == nil || !strings.Contains(err.Error(), "example.io/v1/widgets") || !strings.Contains(err.Error(), "other.io/v1/widgets") {
		t.Fatalf("expected bounded ambiguity candidates, got %v", err)
	}
	got, err := client.Query(context.Background(), QueryRequest{
		ClusterID: "BCS-K8S-10001", Action: "get", Kind: "Widget", Namespace: "default", Name: "example",
		GVR: &ResourceRef{Group: "example.io", Version: "v1beta1", Resource: "widgets"},
	})
	if err != nil || got.GVR.Version != "v1beta1" || got.Items[0].Details["phase"] != "Ready" {
		t.Fatalf("explicit non-preferred GVR = %+v, %v", got, err)
	}
	if discoveryCalls != 6 || resourceCalls != 3 {
		t.Fatalf("unexpected exact discovery counts: discovery=%d resource=%d", discoveryCalls, resourceCalls)
	}
	// 精确版本缓存不能冒充 Kind 的 preferred 映射；随后按 Kind 查询仍需完整 Discovery。
	fresh := NewGatewayClient(client.cfg)
	_, err = fresh.Query(context.Background(), QueryRequest{
		ClusterID: "BCS-K8S-10001", Action: "get", Namespace: "default", Name: "example",
		GVR: &ResourceRef{Group: "example.io", Version: "v1beta1", Resource: "widgets"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fresh.Query(context.Background(), QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "Widget", Namespace: "default"})
	if err == nil || !strings.Contains(err.Error(), "多个 API Group") {
		t.Fatalf("exact GVR cache incorrectly became preferred mapping: %v", err)
	}
	if discoveryCalls != 12 || resourceCalls != 4 {
		t.Fatalf("unexpected refresh counts: discovery=%d resource=%d", discoveryCalls, resourceCalls)
	}

	_, err = client.Query(context.Background(), QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "Secret", Namespace: "default"})
	if err == nil || !strings.Contains(err.Error(), "不允许读取 Secret") {
		t.Fatalf("Secret access was not denied: %v", err)
	}
}

func TestQueryExplicitGVRValidation(t *testing.T) {
	valid := QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", GVR: &ResourceRef{Group: "example.io", Version: "v1", Resource: "widgets"}, Namespace: "default"}
	for _, change := range []func(*QueryRequest){
		func(q *QueryRequest) { q.GVR.Version = "" },
		func(q *QueryRequest) { q.GVR.Resource = "" },
		func(q *QueryRequest) { q.GVR.Group = "../bad" },
		func(q *QueryRequest) { q.GVR.Version = "v1/bad" },
		func(q *QueryRequest) { q.GVR.Resource = "widgets/status" },
		func(q *QueryRequest) { q.Kind = "Widget/Status" },
	} {
		q := valid
		copyGVR := *valid.GVR
		q.GVR = &copyGVR
		change(&q)
		if err := q.NormalizeAndValidate(); err == nil {
			t.Errorf("accepted invalid GVR: %+v", q)
		}
	}
}
