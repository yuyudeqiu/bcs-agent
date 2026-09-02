package bcs

import (
	"github.com/cloudwego/eino/components/tool"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
)

func NewTools(client bcsclient.Client) []tool.BaseTool {
	return []tool.BaseTool{
		&ListProjectsTool{client: client},
		&ListClustersTool{client: client},
		&GetClusterDetailTool{client: client},
	}
}
