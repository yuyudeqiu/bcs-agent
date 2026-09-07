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
	output, err = tool.InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-10001","action":"list","gvr":{"group":"example.io","version":"v1","resource":"widgets"},"namespace":"default"}`)
	if err != nil || client.request.Kind != "" || client.request.GVR == nil || client.request.GVR.Resource != "widgets" {
		t.Fatalf("explicit GVR result=%s request=%+v err=%v", output, client.request, err)
	}
	for _, args := range []string{`{`, `null`, `[]`, `{}`, `{"cluster_id":1}`, `{"cluster_id":"BCS-K8S-10001","action":"delete","kind":"Pod","namespace":"default"}`, `{"cluster_id":"BCS-K8S-10001","action":"get","kind":"Node"}`, `{"cluster_id":"BCS-K8S-10001","action":"list","gvr":{"group":"example.io","version":"v1"},"namespace":"default"}`, `{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Node","approved":true}`, `{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Node"} {}`} {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Errorf("accepted: %s", args)
		}
	}
	if client.calls != 2 {
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

func TestKubernetesQueryNameContains(t *testing.T) {
	client := &queryClient{}
	queryTool := &KubernetesQueryTool{client: client}
	args := `{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Pod","namespace":"default","name_contains":"cwlicense","continue":"next","limit":100}`
	if _, err := queryTool.InvokableRun(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if client.request.NameContains != "cwlicense" || client.request.Continue != "next" || client.request.Limit != 100 {
		t.Fatalf("filter parameters lost: %+v", client.request)
	}
	for _, invalid := range []string{
		`{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Pod","namespace":"default","name_contains":123}`,
		`{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Pod","namespace":"default","name_contains":"cw*"}`,
		`{"cluster_id":"BCS-K8S-10001","action":"get","kind":"Pod","namespace":"default","name":"cwlicense","name_contains":"cw"}`,
	} {
		if _, err := queryTool.InvokableRun(context.Background(), invalid); err == nil {
			t.Errorf("accepted %s", invalid)
		}
	}
	if client.calls != 1 {
		t.Fatalf("invalid filter reached client: %d", client.calls)
	}
	output, err := (&KubernetesQueryTool{client: kubeclient.NewMockClient()}).InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-40890","action":"list","kind":"Pod","all_namespaces":true,"name_contains":"web-1","limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var result kubeclient.QueryResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || result.Items[0].Name != "web-1" || result.ScannedCount != 2 || result.NameContains != "web-1" {
		t.Fatalf("unexpected filtered output: %s", output)
	}
}

func TestKubernetesQueryOutputParameter(t *testing.T) {
	client := &queryClient{}
	queryTool := &KubernetesQueryTool{client: client}
	for _, output := range []string{"", "full", ""} {
		params := map[string]any{"cluster_id": "BCS-K8S-10001", "action": "get", "kind": "Pod", "namespace": "default", "name": "web"}
		if output != "" {
			params["output"] = output
		}
		arguments, _ := json.Marshal(params)
		if _, err := queryTool.InvokableRun(context.Background(), string(arguments)); err != nil {
			t.Fatal(err)
		}
		want := output
		if want == "" {
			want = "summary"
		}
		if client.request.Output != want {
			t.Fatalf("output=%q, want %q", client.request.Output, want)
		}
	}
	for _, arguments := range []string{
		`{"cluster_id":"BCS-K8S-10001","action":"list","kind":"Node","output":"full"}`,
		`{"cluster_id":"BCS-K8S-10001","action":"get","kind":"Node","output":"full"}`,
		`{"cluster_id":"BCS-K8S-10001","action":"get","kind":"Node","name":"worker","output":"raw"}`,
		`{"cluster_id":"BCS-K8S-10001","action":"get","kind":"Node","name":"worker","output":true}`,
	} {
		if _, err := queryTool.InvokableRun(context.Background(), arguments); err == nil {
			t.Errorf("accepted: %s", arguments)
		}
	}
	if client.calls != 3 {
		t.Fatalf("invalid output parameters reached client: %d", client.calls)
	}

	mockTool := &KubernetesQueryTool{client: kubeclient.NewMockClient()}
	output, err := mockTool.InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-40890","action":"get","kind":"Widget","namespace":"default","name":"example","output":"full"}`)
	if err != nil {
		t.Fatal(err)
	}
	var result kubeclient.QueryResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Output != "full" || result.Source != "mock" || result.Count != 1 || result.Items[0].Resource["spec"] == nil || result.Items[0].Details != nil {
		t.Fatalf("tool did not serialize full resource: %s", output)
	}
}
