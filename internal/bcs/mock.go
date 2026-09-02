package bcs

import (
	"context"
	"fmt"
)

type MockClient struct{}

func NewMockClient() *MockClient {
	return &MockClient{}
}

func (c *MockClient) ListProjects(_ context.Context) ([]Project, error) {
	return []Project{
		{ProjectID: "proj-001", ProjectCode: "bcs-prod", Name: "生产项目", Creator: "admin"},
		{ProjectID: "proj-002", ProjectCode: "bcs-staging", Name: "预发布项目", Creator: "admin"},
	}, nil
}

func (c *MockClient) ListClusters(_ context.Context, _ string) ([]Cluster, error) {
	return []Cluster{
		{ID: "BCS-K8S-40888", Name: "prod-bj-cluster", Status: "Running", Kubernetes: "v1.28.3", Environment: "prod", NodeCount: 12},
		{ID: "BCS-K8S-40889", Name: "staging-sh-cluster", Status: "Running", Kubernetes: "v1.28.3", Environment: "stag", NodeCount: 5},
		{ID: "BCS-K8S-40890", Name: "dev-gz-cluster", Status: "Warning", Kubernetes: "v1.28.3", Environment: "debug", NodeCount: 3},
	}, nil
}

func (c *MockClient) GetCluster(_ context.Context, clusterID string) (ClusterDetail, error) {
	if clusterID == "" {
		return ClusterDetail{}, fmt.Errorf("cluster_id 不能为空")
	}
	return ClusterDetail{
		ID:              clusterID,
		Kubernetes:      "v1.28.3",
		CNI:             "Flannel",
		Region:          "华北-北京",
		DeploymentCount: 32,
	}, nil
}
