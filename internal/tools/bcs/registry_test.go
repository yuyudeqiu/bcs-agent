package bcs

import (
	"context"
	"testing"

	bcsclient "github.com/yuyudeqiu/bcs-agent/internal/bcs"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
)

func TestReadOnlyToolsExcludeScale(t *testing.T) {
	tools := NewReadOnlyTools(bcsclient.NewMockClient(), kubeclient.NewMockClient())
	for _, candidate := range tools {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == "kubernetes_scale" {
			t.Fatal("read-only tools include kubernetes_scale")
		}
	}
	if len(NewTools(bcsclient.NewMockClient(), kubeclient.NewMockClient())) != len(tools)+1 {
		t.Fatal("default tool set should include exactly one additional write tool")
	}
}
