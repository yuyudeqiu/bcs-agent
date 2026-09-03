package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

func TestGatewayInputAndCancellation(t *testing.T) {
	client := NewGatewayClient(config.BCSConfig{BaseURL: "https://bcs.example.com", APIToken: "test-token"})
	for _, id := range []string{"", " ", "..", "../other", "a/b", "a?x=y", "a%2Fb"} {
		if _, err := client.restConfig(id); err == nil {
			t.Errorf("invalid cluster ID %q accepted", id)
		}
	}
	for _, cfg := range []config.BCSConfig{
		{BaseURL: "https://bcs.example.com"},
		{BaseURL: "", APIToken: "test-token"},
		{BaseURL: "https://bcs.example.com?token=x", APIToken: "test-token"},
	} {
		if _, err := NewGatewayClient(cfg).restConfig("BCS-K8S-10001"); err == nil {
			t.Error("invalid gateway config accepted")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("canceled request reached server")
		fmt.Fprint(w, "{}")
	}))
	defer server.Close()
	client = NewGatewayClient(config.BCSConfig{BaseURL: server.URL, APIToken: "test-token"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Query(ctx, QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "Node"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
}
