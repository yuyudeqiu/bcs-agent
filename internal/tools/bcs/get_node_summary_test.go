package bcs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

type nodeSummaryClient struct {
	id    string
	calls int
	err   error
}

func (c *nodeSummaryClient) GetNodeSummary(_ context.Context, id string) (kubeclient.NodeSummary, error) {
	c.id = id
	c.calls++
	return kubeclient.NodeSummary{ClusterID: id, TotalNodes: 3, ReadyNodes: 2, NotReadyNodes: 1, Source: "kubernetes"}, c.err
}

func TestGetClusterNodeSummaryTool(t *testing.T) {
	client := &nodeSummaryClient{}
	tool := &GetClusterNodeSummaryTool{client: client}
	output, err := tool.InvokableRun(context.Background(), `{"cluster_id":" BCS-K8S-10001 "}`)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(output), &fields); err != nil {
		t.Fatal(err)
	}
	if client.id != "BCS-K8S-10001" || fields["cluster_id"] != client.id || fields["total_nodes"] != float64(3) || fields["ready_nodes"] != float64(2) || fields["not_ready_nodes"] != float64(1) {
		t.Fatalf("unexpected summary: %s, client ID %s", output, client.id)
	}
	for _, args := range []string{`{`, `{}`, `{"cluster_id":" "}`, `{"cluster_id":42}`} {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Errorf("invalid arguments accepted: %s", args)
		}
	}
	if client.calls != 1 {
		t.Fatalf("invalid arguments reached client, calls = %d", client.calls)
	}
	client.err = errors.New("access denied")
	if output, err := tool.InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-10001"}`); !errors.Is(err, client.err) || output != "" {
		t.Fatalf("failed request produced output %s, error %v", output, err)
	}
}

func TestNodeSummaryRegisteredWithMock(t *testing.T) {
	for _, tool := range NewTools(bcsclient.NewMockClient(), kubeclient.NewMockClient()) {
		info, err := tool.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != "get_cluster_node_summary" {
			continue
		}
		output, err := tool.(*GetClusterNodeSummaryTool).InvokableRun(context.Background(), `{"cluster_id":"BCS-K8S-40890"}`)
		if err != nil {
			t.Fatal(err)
		}
		var got kubeclient.NodeSummary
		if err := json.Unmarshal([]byte(output), &got); err != nil {
			t.Fatal(err)
		}
		if got.Source != "mock" || got.TotalNodes != 3 || got.ReadyNodes != 2 || got.NotReadyNodes != 1 {
			t.Fatalf("unexpected mock summary: %#v", got)
		}
		return
	}
	t.Fatal("node summary tool not registered")
}
