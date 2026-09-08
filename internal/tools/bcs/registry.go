package bcs

import (
	"github.com/cloudwego/eino/components/tool"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

func NewTools(client bcsclient.Client, kubernetesClient kubeclient.Client) []tool.BaseTool {
	return append(NewReadOnlyTools(client, kubernetesClient), &KubernetesScaleTool{client: kubernetesClient})
}

func NewReadOnlyTools(client bcsclient.Client, kubernetesClient kubeclient.Client) []tool.BaseTool {
	return []tool.BaseTool{
		&ListProjectsTool{client: client},
		&ListClustersTool{client: client},
		&GetClusterDetailTool{client: client},
		&KubernetesQueryTool{client: kubernetesClient},
		&KubernetesLogsTool{client: kubernetesClient},
	}
}
