package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func nameSearchClient(t *testing.T, names []string) (*GatewayClient, *int) {
	t.Helper()
	calls := 0
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if serveDiscovery(w, r) {
			return
		}
		calls++
		if r.URL.Path != "/gateway/clusters/BCS-K8S-10001/api/v1/namespaces/default/pods" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// 名称包含不是 Kubernetes fieldSelector；不能发送无效的服务端过滤表达式。
		if r.URL.Query().Get("fieldSelector") != "" {
			t.Errorf("unexpected selector: %s", r.URL)
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit != 100 {
			t.Errorf("limit changed: %s", r.URL)
		}
		offset := 0
		if token := r.URL.Query().Get("continue"); token != "" {
			offset, err = strconv.Atoi(token)
			if err != nil || offset >= len(names) {
				t.Errorf("invalid token: %s", token)
				return
			}
		}
		end := min(offset+limit, len(names))
		items := []any{}
		for _, name := range names[offset:end] {
			items = append(items, map[string]any{"metadata": map[string]any{"name": name, "namespace": "default"}})
		}
		metadata := map[string]any{}
		if end < len(names) {
			metadata["continue"] = strconv.Itoa(end)
		}
		json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": metadata, "items": items})
	})
	return client, &calls
}

func searchRequest() QueryRequest {
	return QueryRequest{ClusterID: "BCS-K8S-10001", Action: "list", Kind: "Pod", Namespace: "default", NameContains: "cwlicense", Limit: 100}
}

func searchNames(count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("unrelated-%04d", i)
	}
	return names
}

func TestNameContainsSkipsUnmatchedPages(t *testing.T) {
	names := searchNames(350)
	names[220], names[299], names[300] = "cwlicense-a", "cwlicense-b", "cwlicense-c"
	client, calls := nameSearchClient(t, names)
	q := searchRequest()
	got, err := client.Query(context.Background(), q)
	if err != nil || *calls != 3 || got.Count != 2 || got.ScannedCount != 300 || !got.HasMore || got.Continue != "300" || got.ScanLimitReached {
		t.Fatalf("result=%+v calls=%d err=%v", got, *calls, err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "unrelated-") || got.NameContains != "cwlicense" {
		t.Fatalf("unmatched resources reached output: %s", encoded)
	}
	q.Continue = got.Continue
	next, err := client.Query(context.Background(), q)
	if err != nil || next.HasMore || next.Count != 1 || next.Items[0].Name != "cwlicense-c" || next.ScannedCount != 50 || *calls != 4 {
		t.Fatalf("lost match at page boundary: %+v %v", next, err)
	}
}

func TestNameContainsBoundedScanAndContinuation(t *testing.T) {
	names := searchNames(1050)
	names[1020] = "cwlicense-late"
	client, calls := nameSearchClient(t, names)
	q := searchRequest()
	got, err := client.Query(context.Background(), q)
	if err != nil || *calls != 10 || got.Count != 0 || got.Items == nil || !got.HasMore || got.Continue != "1000" || got.ScannedCount != 1000 || !got.ScanLimitReached {
		t.Fatalf("scan bound: %+v calls=%d err=%v", got, *calls, err)
	}
	q.Continue = got.Continue
	got, err = client.Query(context.Background(), q)
	if err != nil || got.HasMore || got.Count != 1 || got.ScanLimitReached || got.Items[0].Name != "cwlicense-late" {
		t.Fatalf("resume: %+v %v", got, err)
	}
	q = searchRequest()
	q.NameContains = "CWLICENSE"
	client, _ = nameSearchClient(t, []string{"cwlicense-a"})
	got, err = client.Query(context.Background(), q)
	if err != nil || got.Count != 0 || got.HasMore || got.ScannedCount != 1 {
		t.Fatalf("literal case-sensitive matching: %+v %v", got, err)
	}
}

func TestNameContainsErrorsAndEmptyPageBound(t *testing.T) {
	for _, mode := range []string{"cycle", "expired", "bad identity", "empty pages"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if serveDiscovery(w, r) {
					return
				}
				calls++
				if mode == "expired" && calls == 2 {
					w.WriteHeader(http.StatusGone)
					fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Expired","code":410}`)
					return
				}
				token := strconv.Itoa(calls)
				if mode == "cycle" && calls == 3 {
					token = "1"
				}
				items := []any{}
				if mode == "bad identity" {
					items = append(items, map[string]any{"metadata": map[string]any{"name": "unrelated", "namespace": "other"}})
				}
				json.NewEncoder(w).Encode(map[string]any{"kind": "PodList", "apiVersion": "v1", "metadata": map[string]any{"continue": token}, "items": items})
			})
			q := searchRequest()
			q.Limit = 1
			got, err := client.Query(context.Background(), q)
			if mode == "empty pages" {
				if err != nil || calls != 20 || !got.HasMore || !got.ScanLimitReached || got.Count != 0 {
					t.Fatalf("empty-page loop: %+v calls=%d err=%v", got, calls, err)
				}
			} else {
				if err == nil || got.Items != nil || got.Source != "" {
					t.Fatalf("failure returned success: %+v %v", got, err)
				}
				if mode == "expired" && !apierrors.IsResourceExpired(err) {
					t.Fatalf("lost expiry cause: %v", err)
				}
			}
		})
	}
}

func TestNameContainsValidationAndMock(t *testing.T) {
	for _, fragment := range []string{" ", "cw license", "*license", "cw/license", strings.Repeat("a", 254)} {
		q := searchRequest()
		q.NameContains = fragment
		if err := q.NormalizeAndValidate(); err == nil {
			t.Errorf("accepted %q", fragment)
		}
	}
	q := searchRequest()
	q.Action = "get"
	q.Name = "cwlicense"
	q.Limit = 0
	if err := q.NormalizeAndValidate(); err == nil {
		t.Fatal("get accepted name_contains")
	}
	q = searchRequest()
	q.Name = "cwlicense"
	if err := q.NormalizeAndValidate(); err == nil {
		t.Fatal("list accepted both exact and partial name")
	}
	q = QueryRequest{ClusterID: "BCS-K8S-40890", Action: "list", Kind: "Pod", AllNamespaces: true, NameContains: "web-1", Limit: 1}
	got, err := NewMockClient().Query(context.Background(), q)
	if err != nil || got.Count != 1 || got.ScannedCount != 2 || got.HasMore || got.Items[0].Namespace != "production" {
		t.Fatalf("Mock search: %+v %v", got, err)
	}
	q.NameContains = "web"
	first, err := NewMockClient().Query(context.Background(), q)
	if err != nil || first.Count != 1 || !first.HasMore || first.Continue != "1" {
		t.Fatalf("Mock first page: %+v %v", first, err)
	}
	q.Continue = first.Continue
	last, err := NewMockClient().Query(context.Background(), q)
	if err != nil || last.Count != 1 || last.HasMore || last.Items[0].Name != "web-1" {
		t.Fatalf("Mock next page: %+v %v", last, err)
	}
}
