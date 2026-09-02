package bcs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

func TestListClustersToolFromProjects(t *testing.T) {
	clusterCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/bcsapi/v4/bcsproject/v1/projects":
			_, _ = w.Write([]byte(`{"code":0,"data":{"total":1,"results":[{"projectID":"p1","projectCode":"demo","name":"演示项目"}]}}`))
		case "/bcsapi/v4/clustermanager/v1/cluster":
			clusterCalls++
			if got := r.URL.Query().Get("projectID"); got != "p1" {
				t.Errorf("projectID = %q, want p1", got)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":[{"clusterID":"BCS-K8S-10001","clusterName":"demo","status":"RUNNING","environment":"prod","clusterBasicSettings":{"version":"v1.23.17"}}]}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := bcsclient.NewHTTPClient(config.BCSConfig{BaseURL: server.URL})
	projectsTool := &ListProjectsTool{client: client}
	output, err := projectsTool.InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	var projects []bcsclient.Project
	if err := json.Unmarshal([]byte(output), &projects); err != nil || len(projects) != 1 {
		t.Fatalf("projects = %#v, error = %v", projects, err)
	}
	arguments, err := json.Marshal(map[string]string{"project_id": projects[0].ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	clustersTool := &ListClustersTool{client: client}
	output, err = clustersTool.InvokableRun(context.Background(), string(arguments))
	if err != nil {
		t.Fatal(err)
	}
	var clusters []bcsclient.Cluster
	if err := json.Unmarshal([]byte(output), &clusters); err != nil || len(clusters) != 1 || clusters[0].ID != "BCS-K8S-10001" {
		t.Fatalf("clusters = %#v, error = %v", clusters, err)
	}
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields[0]["kubernetes_version"]) != `"v1.23.17"` || string(fields[0]["environment"]) != `"prod"` {
		t.Fatalf("tool output missing version or environment: %s", output)
	}
	for _, args := range []string{`{`, `{"project_id":123}`, `{"project_id":[]}`} {
		if _, err := clustersTool.InvokableRun(context.Background(), args); err == nil {
			t.Errorf("expected invalid arguments error for %q", args)
		}
	}
	if clusterCalls != 1 {
		t.Errorf("cluster calls = %d, want 1", clusterCalls)
	}
}

func TestListClustersToolWithoutProject(t *testing.T) {
	for _, args := range []string{`{}`, `{"project_id":""}`, `{"project_id":"  "}`} {
		t.Run(args, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.RequestURI != "/bcsapi/v4/clustermanager/v1/cluster" {
					t.Errorf("unexpected request: %s %s", r.Method, r.RequestURI)
				}
				_, _ = w.Write([]byte(`{"code":0,"data":[
					{"clusterID":"BCS-K8S-10001","clusterName":"first","projectID":"p1","status":"RUNNING"},
					{"clusterID":"BCS-K8S-10002","clusterName":"second","projectID":"p2","status":"RUNNING"}
				]}`))
			}))
			defer server.Close()
			client := bcsclient.NewHTTPClient(config.BCSConfig{BaseURL: server.URL})
			tool := &ListClustersTool{client: client}
			output, err := tool.InvokableRun(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			var clusters []bcsclient.Cluster
			if err := json.Unmarshal([]byte(output), &clusters); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(clusters) != 2 || clusters[0].ID != "BCS-K8S-10001" || clusters[1].ID != "BCS-K8S-10002" {
				t.Fatalf("calls = %d, clusters = %#v", calls, clusters)
			}
		})
	}
}
