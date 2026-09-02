package bcs

import (
	"context"
	"net/http"
	"net/http/httptest"
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
