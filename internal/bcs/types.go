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
