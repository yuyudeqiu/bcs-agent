package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const logPodPath = "/gateway/clusters/BCS-K8S-10001/api/v1/namespaces/default/pods/web"
const logPod = `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"web","namespace":"default"},"spec":{"containers":[{"name":"app"}]}}`

func logInt(value int64) *int64 { return &value }
func logRequest() LogsRequest {
	return LogsRequest{ClusterID: "BCS-K8S-10001", Namespace: "default", Pod: "web"}
}

func TestGatewayLogs(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			calls := 0
			client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch r.URL.Path {
				case logPodPath:
					fmt.Fprint(w, logPod)
				case logPodPath + "/log":
					q := r.URL.Query()
					if q.Get("container") != "app" || q.Get("timestamps") != "true" || q.Get("limitBytes") != "32769" || q.Get("follow") == "true" || r.Header.Get("Accept") != "text/plain" {
						t.Errorf("unexpected log request: %s", r.URL)
					}
					wantTail := "200"
					if explicit {
						wantTail = "10"
						if q.Get("sinceSeconds") != "60" || q.Get("previous") != "true" {
							t.Errorf("missing log options: %s", r.URL)
						}
					} else if q.Get("sinceSeconds") != "" || q.Get("previous") == "true" {
						t.Errorf("unexpected log options: %s", r.URL)
					}
					if q.Get("tailLines") != wantTail {
						t.Errorf("tailLines = %s", q.Get("tailLines"))
					}
					w.Header().Set("Content-Type", "text/plain")
					fmt.Fprint(w, "2026-09-03T00:00:00Z app started\n")
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			q := logRequest()
			if explicit {
				q.Container = "app"
				q.TailLines = logInt(10)
				q.SinceSeconds = logInt(60)
				q.Previous = true
			}
			got, err := client.Logs(context.Background(), q)
			if err != nil || calls != 2 || got.Container != "app" || got.Source != "kubernetes" || got.Truncated || got.Redacted || !strings.Contains(got.Logs, "app started") || !got.Timestamps {
				t.Fatalf("got=%+v err=%v calls=%d", got, err, calls)
			}
		})
	}
}

func TestLogsValidationAndContainers(t *testing.T) {
	calls := 0
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
	for _, change := range []func(*LogsRequest){
		func(q *LogsRequest) { q.ClusterID = "../bad" }, func(q *LogsRequest) { q.Namespace = "" },
		func(q *LogsRequest) { q.Pod = "" }, func(q *LogsRequest) { q.Pod = "web/log" },
		func(q *LogsRequest) { q.Container = "a/b" }, func(q *LogsRequest) { q.TailLines = logInt(0) },
		func(q *LogsRequest) { q.TailLines = logInt(-1) }, func(q *LogsRequest) { q.TailLines = logInt(1001) },
		func(q *LogsRequest) { q.SinceSeconds = logInt(0) }, func(q *LogsRequest) { q.SinceSeconds = logInt(-1) },
	} {
		q := logRequest()
		change(&q)
		if _, err := client.Logs(context.Background(), q); err == nil {
			t.Errorf("accepted invalid request: %+v", q)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid input reached server: %d", calls)
	}
	var pod corev1.Pod
	if err := json.Unmarshal([]byte(logPod), &pod); err != nil {
		t.Fatal(err)
	}
	pod.Spec.InitContainers = []corev1.Container{{Name: "init"}}
	pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug"}}}
	for _, name := range []string{"app", "init", "debug"} {
		if got, err := selectLogContainer(&pod, name); err != nil || got != name {
			t.Fatalf("container=%s err=%v", got, err)
		}
	}
	for _, name := range []string{"", "missing"} {
		if _, err := selectLogContainer(&pod, name); err == nil || !strings.Contains(err.Error(), "app、init、debug") {
			t.Fatalf("expected candidates: %v", err)
		}
	}
	client = queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != logPodPath {
			t.Error("ambiguous target reached logs endpoint")
		}
		json.NewEncoder(w).Encode(pod)
	})
	if _, err := client.Logs(context.Background(), logRequest()); err == nil {
		t.Fatal("ambiguous container accepted")
	}
}

func TestLogsOutputBoundsAndRedaction(t *testing.T) {
	q := logRequest()
	q.NormalizeAndValidate()
	q.Container = "app"
	for _, test := range []struct {
		name, body string
		truncated  bool
	}{
		{"empty", "", false},
		{"credentials", "password=private-pass token=private-token Authorization: Bearer private-auth\n{\"api_key\":\"private-key\"}\ntest-token\n-----BEGIN PRIVATE KEY-----\nprivate-pem\n-----END PRIVATE KEY-----\n", false},
		{"long line", strings.Repeat("x", maxLogBytes+1), true},
		{"escaped", strings.Repeat("\x00", maxLogBytes-1) + "\n", true},
		{"unicode", strings.Repeat("日志", maxLogBytes/6) + "\n", true},
		{"too many lines", strings.Repeat("line\n", 201), true},
		{"boundary secret", "safe\npassword=" + strings.Repeat("s", maxLogBytes), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := formatLogs(q, []byte(test.body), "test-token", "kubernetes")
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got)
			if err != nil || len(encoded) > maxLogBytes || got.Truncated != test.truncated || !utf8.ValidString(got.Logs) {
				t.Fatalf("len=%d truncated=%v err=%v", len(encoded), got.Truncated, err)
			}
			if test.name == "credentials" && (!got.Redacted || strings.Contains(got.Logs, "private-") || strings.Contains(got.Logs, "test-token")) {
				t.Fatalf("credentials leaked: %s", got.Logs)
			}
			if test.name == "boundary secret" && got.Logs != "safe\n" {
				t.Fatal("partial credential line retained")
			}
		})
	}
	oversized := q
	oversized.ClusterID = strings.Repeat("a", maxLogBytes)
	if got, err := formatLogs(oversized, nil, "", "kubernetes"); err == nil || got.Logs != "" {
		t.Fatal("oversized metadata bypassed output bound")
	}
	// 服务端忽略字节限制时，本地只读取有界前缀，仍明确报告截断。
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == logPodPath {
			fmt.Fprint(w, logPod)
			return
		}
		fmt.Fprint(w, "safe\n"+strings.Repeat("x", maxLogBytes*4))
	})
	got, err := client.Logs(context.Background(), q)
	if err != nil || !got.Truncated || got.Logs != "safe\n" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestLogsFailureAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		body       string
		podFailure bool
	}{
		{"log forbidden", 403, `{"kind":"Status","apiVersion":"v1","reason":"Forbidden","code":403,"message":"private-secret test-token"}`, false},
		{"pod missing", 404, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`, true},
		{"previous missing", 400, `{"kind":"Status","apiVersion":"v1","reason":"BadRequest","code":400,"message":"private-secret"}`, false},
		{"invalid pod", 200, `invalid private-secret`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == logPodPath && !test.podFailure {
					fmt.Fprint(w, logPod)
					return
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			})
			got, err := client.Logs(context.Background(), logRequest())
			if err == nil || !reflect.DeepEqual(got, LogsResult{}) || strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "test-token") {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if test.status == 403 && !apierrors.IsForbidden(err) {
				t.Fatalf("lost error cause: %v", err)
			}
			if test.status == 404 && !apierrors.IsNotFound(err) {
				t.Fatalf("lost error cause: %v", err)
			}
		})
	}
	client := queryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == logPodPath {
			fmt.Fprint(w, logPod)
			return
		}
		w.Write([]byte("partial-private-secret\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	got, err := client.Logs(ctx, logRequest())
	if !errors.Is(err, context.DeadlineExceeded) || got.Logs != "" {
		t.Fatalf("partial timeout: %+v %v", got, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := client.Logs(ctx, logRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestMockLogs(t *testing.T) {
	client := NewMockClient()
	q := LogsRequest{ClusterID: "BCS-K8S-40890", Namespace: "default", Pod: "web-0", TailLines: logInt(1), SinceSeconds: logInt(1)}
	got, err := client.Logs(context.Background(), q)
	if err != nil || got.Source != "mock" || got.Container != "web" || !strings.Contains(got.Logs, "listening") || strings.Contains(got.Logs, "started") {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	q.Previous = true
	if _, err := client.Logs(context.Background(), q); err == nil {
		t.Fatal("Mock fabricated previous logs")
	}
	q.Previous = false
	q.Pod = "missing"
	if _, err := client.Logs(context.Background(), q); !apierrors.IsNotFound(err) {
		t.Fatalf("unknown pod=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Logs(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatalf("Mock cancellation=%v", err)
	}
}
