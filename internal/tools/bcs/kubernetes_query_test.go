package bcs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

type queryClient struct {
	kubeclient.Client
	calls   int
	request kubeclient.QueryRequest
	err     error
}

func (c *queryClient) Query(_ context.Context, q kubeclient.QueryRequest) (kubeclient.QueryResult, error) {
	c.calls++
	c.request = q
	return kubeclient.QueryResult{Source: "kubernetes", Items: []kubeclient.ResourceSummary{}}, c.err
}

func TestKubernetesQueryTool(t *testing.T) {
	client := &queryClient{}
	tool := &KubernetesQueryTool{client: client}
	output, err := tool.InvokableRun(context.Background(), `{"cluster_id":" BCS-K8S-10001 ","action":"list","kind":"Pod","namespace":" default "}`)
	if err != nil || !json.Valid([]byte(output)) || client.request.Namespace != "default" || client.request.Limit != 50 || client.request.ClusterID != "BCS-K8S-10001" {
		t.Fatalf("result=%s request=%+v err=%v", output, client.request, err)
	}
	for _, args := range []string{`{`, `null`, `[]`, `{}`, `{"cluster_id":1}`, `{"cluster_id":"BCS-K8S-10001","action":"delete","kind":"Pod","namespace":"default"}`, `{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Pod"}`, `{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Node","namespace":"default"}`, `{"cluster_id":"BCS-K8S-10001","action":"get","kind":"Node"}`, `{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Node","approved":true}`, `{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Node"} {}`} {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Errorf("accepted: %s", args)
		}
	}
	if client.calls != 1 {
		t.Fatalf("invalid arguments reached client: %d", client.calls)
	}
	client.err = errors.New("test error")
	if output, err := tool.InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Node"}`); output != "" || !errors.Is(err, client.err) {
		t.Fatalf("output=%s error=%v", output, err)
	}
}

func TestKubernetesQueryRegistered(t *testing.T) {
	for _, tool := range NewTools(bcsclient.NewMockClient(), kubeclient.NewMockClient()) {
		info, err := tool.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != "kubernetes_query" {
			continue
		}
		output, err := tool.(*KubernetesQueryTool).InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-40890","action":"list","kind":"Pod","all_namespaces":true}`)
		if err != nil {
			t.Fatal(err)
		}
		var got kubeclient.QueryResult
		if err := json.Unmarshal([]byte(output), &got); err != nil {
			t.Fatal(err)
		}
		if got.Source != "mock" || got.Count != 2 {
			t.Fatalf("unexpected result: %s", output)
		}
		return
	}
	t.Fatal("kubernetes_query not registered")
}
