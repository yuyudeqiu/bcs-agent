package bcs

type Cluster struct {
	ID        string `json:"cluster_id"`
	Name      string `json:"cluster_name"`
	Status    string `json:"status"`
	NodeCount int    `json:"node_count"`
}

type ClusterDetail struct {
	ID              string `json:"cluster_id"`
	Kubernetes      string `json:"kubernetes_version"`
	CNI             string `json:"cni"`
	Region          string `json:"region"`
	DeploymentCount int    `json:"deployment_count"`
}

// Project 是 BCS 项目，字段名与真实 bcsproject API 返回一致。
type Project struct {
	ProjectID    string `json:"projectID"`
	ProjectCode  string `json:"projectCode"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Kind         string `json:"kind"`
	BusinessID   string `json:"businessID"`
	BusinessName string `json:"businessName"`
	Creator      string `json:"creator"`
	CreateTime   string `json:"createTime"`
	IsOffline    bool   `json:"isOffline"`
	UseBKRes     bool   `json:"useBKRes"`
}
