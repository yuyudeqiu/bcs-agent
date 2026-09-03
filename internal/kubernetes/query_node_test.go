package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestQueryNodeReadinessAcrossPages(t *testing.T) {
	calls := 0
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if serveDiscovery(w, r) {
			return
		}
		calls++
		if r.URL.Path != "/gateway/clusters/BCS-K8S-10001/api/v1/nodes" || r.URL.Query().Get("limit") != "2" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		switch r.URL.Query().Get("continue") {
		case "":
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"NodeList","metadata":{"continue":"next+/="},"items":[
				{"metadata":{"name":"control-plane"},"spec":{"unschedulable":true},"status":{"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"True"}]}},
				{"metadata":{"name":"worker-1"},"status":{"conditions":[{"type":"Ready","status":"False"}]}}
			]}`)
		case "next+/=":
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"NodeList","items":[
				{"metadata":{"name":"worker-2"},"status":{"conditions":[{"type":"Ready","status":"Unknown"}]}},
				{"metadata":{"name":"worker-3"},"status":{"conditions":[]}}
			]}`)
		default:
			t.Errorf("unexpected continue: %s", r.URL.RawQuery)
		}
	})
	q := QueryRequest{ClusterID: "BCS-K8S-10001", Kind: "Node", Action: "list", Limit: 2}
	first, err := client.Query(context.Background(), q)
	if err != nil || first.Count != 2 || !first.HasMore || first.Continue != "next+/=" {
		t.Fatalf("first=%+v, error=%v", first, err)
	}
	if fmt.Sprint(first.Items[0].Details["ready"]) != "True" || first.Items[0].Details["unschedulable"] != true || fmt.Sprint(first.Items[1].Details["ready"]) != "False" {
		t.Fatalf("unexpected node readiness: %+v", first.Items)
	}
	q.Continue = first.Continue
	last, err := client.Query(context.Background(), q)
	if err != nil || last.Count != 2 || last.HasMore || calls != 2 {
		t.Fatalf("last=%+v, error=%v, calls=%d", last, err, calls)
	}
	for _, item := range last.Items {
		if fmt.Sprint(item.Details["ready"]) != "Unknown" {
			t.Fatalf("unexpected readiness: %+v", item)
		}
	}
}

func TestMockQueryNodeFixtures(t *testing.T) {
	client := NewMockClient()
	for _, test := range []struct {
		clusterID    string
		total, ready int
	}{
		{"BCS-K8S-40888", 12, 12},
		{"BCS-K8S-40889", 5, 5},
		{"BCS-K8S-40890", 3, 2},
	} {
		q := QueryRequest{ClusterID: test.clusterID, Kind: "Node", Action: "list", Limit: 2}
		total, ready := 0, 0
		for page := 0; page < 10; page++ {
			got, err := client.Query(context.Background(), q)
			if err != nil || got.Source != "mock" {
				t.Fatalf("result=%+v, error=%v", got, err)
			}
			total += got.Count
			for _, item := range got.Items {
				if fmt.Sprint(item.Details["ready"]) == "True" {
					ready++
				}
			}
			if !got.HasMore {
				break
			}
			q.Continue = got.Continue
		}
		if total != test.total || ready != test.ready {
			t.Fatalf("%s: total=%d, ready=%d", test.clusterID, total, ready)
		}
	}
	q := QueryRequest{ClusterID: "BCS-K8S-missing", Kind: "Pod", Namespace: "default", Action: "list"}
	if _, err := client.Query(context.Background(), q); err == nil {
		t.Fatal("unknown Mock cluster accepted")
	}
	q.ClusterID = "BCS-K8S-40890"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Query(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
