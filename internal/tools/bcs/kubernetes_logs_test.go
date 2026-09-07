package bcs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

type logsClient struct {
	kubeclient.Client
	calls   int
	request kubeclient.LogsRequest
	err     error
}

func (c *logsClient) Logs(_ context.Context, q kubeclient.LogsRequest) (kubeclient.LogsResult, error) {
	c.calls++
	c.request = q
	return kubeclient.LogsResult{Source: "kubernetes", Logs: "example"}, c.err
}

func TestKubernetesLogsTool(t *testing.T) {
	client := &logsClient{}
	tool := &KubernetesLogsTool{client: client}
	args := `{"cluster_id":" BCS-K8S-10001 ","namespace":" default ","pod":" web "}`
	output, err := tool.InvokableRun(context.Background(), args)
	if err != nil || !json.Valid([]byte(output)) || client.request.Pod != "web" || client.request.Namespace != "default" || *client.request.TailLines != 200 {
		t.Fatalf("output=%s request=%+v err=%v", output, client.request, err)
	}
	for _, args := range []string{
		`null`, `{}`, `[]`, `{"cluster_id":"BCS-K8S-10001","namespace":"default"}`,
		`{"cluster_id":"BCS-K8S-10001","namespace":"default","pod":"web","tail_lines":-1}`,
		`{"cluster_id":"BCS-K8S-10001","namespace":"default","pod":"web","tail_lines":0}`,
		`{"cluster_id":"BCS-K8S-10001","namespace":"default","pod":"web","tail_lines":1001}`,
		`{"cluster_id":"BCS-K8S-10001","namespace":"default","pod":"web","tail_lines":"200"}`,
		`{"cluster_id":"BCS-K8S-10001","namespace":"default","pod":"web","follow":true}`,
		`{"cluster_id":"BCS-K8S-10001","namespace":"default","pod":"web","limit_bytes":999999}`,
		`{"cluster_id":"BCS-K8S-10001","namespace":"default","pod":"web"} {}`,
	} {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Errorf("accepted %s", args)
		}
	}
	if client.calls != 1 {
		t.Fatalf("invalid parameters reached client: %d", client.calls)
	}
	client.err = errors.New("read failed")
	if output, err := tool.InvokableRun(context.Background(), args); output != "" || !errors.Is(err, client.err) {
		t.Fatalf("error chain/output: %s %v", output, err)
	}
}

func TestKubernetesLogsRegistered(t *testing.T) {
	for _, tool := range NewTools(bcsclient.NewMockClient(), kubeclient.NewMockClient()) {
		info, err := tool.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != "kubernetes_logs" {
			continue
		}
		output, err := tool.(*KubernetesLogsTool).InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-40890","namespace":"default","pod":"web-0"}`)
		if err != nil {
			t.Fatal(err)
		}
		var result kubeclient.LogsResult
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		if result.Source != "mock" || result.Container != "web" || result.TailLines != 200 || result.Logs == "" {
			t.Fatalf("result=%s", output)
		}
		return
	}
	t.Fatal("kubernetes_logs not registered")
}
