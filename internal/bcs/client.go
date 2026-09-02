package bcs

import "context"

// Client 定义工具需要的 BCS 能力。后续真实 HTTP Client 和测试 Mock 使用同一接口。
type Client interface {
	ListProjects(ctx context.Context) ([]Project, error)
	// ListClusters 的 projectID 可为空，表示不按项目过滤。
	ListClusters(ctx context.Context, projectID string) ([]Cluster, error)
	GetCluster(ctx context.Context, clusterID string) (ClusterDetail, error)
}
