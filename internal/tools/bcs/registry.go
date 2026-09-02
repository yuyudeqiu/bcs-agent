package bcs

import (
	"github.com/cloudwego/eino/components/tool"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

func NewTools(client bcsclient.Client, kubernetesClient kubeclient.Client) []tool.BaseTool {
	return []tool.BaseTool{
		&ListProjectsTool{client: client},
		&ListClustersTool{client: client},
		&GetClusterDetailTool{client: client},
		&GetClusterNodeSummaryTool{client: kubernetesClient},
		&KubernetesQueryTool{client: kubernetesClient},
	}
}
