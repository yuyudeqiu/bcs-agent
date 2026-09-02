package kubernetes

import (
	"context"
	"fmt"
)

type MockClient struct{}

func NewMockClient() *MockClient { return &MockClient{} }

func (c *MockClient) GetNodeSummary(ctx context.Context, clusterID string) (NodeSummary, error) {
	if err := ctx.Err(); err != nil {
		return NodeSummary{}, err
	}
	if err := validateClusterID(clusterID); err != nil {
		return NodeSummary{}, err
	}
	// 与 BCS Mock 列表中的集群及节点数量一致。
	summaries := map[string]NodeSummary{
		"BCS-K8S-40888": {TotalNodes: 12, ReadyNodes: 12},
		"BCS-K8S-40889": {TotalNodes: 5, ReadyNodes: 5},
		"BCS-K8S-40890": {TotalNodes: 3, ReadyNodes: 2, NotReadyNodes: 1},
	}
	summary, ok := summaries[clusterID]
	if !ok {
		return NodeSummary{}, fmt.Errorf("Mock 中不存在集群 %s，请先查询 Mock 集群列表", clusterID)
	}
	summary.ClusterID = clusterID
	summary.Source = "mock"
	return summary, nil
}
