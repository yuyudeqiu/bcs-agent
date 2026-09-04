package cli

import "testing"

func TestFormatApproval(t *testing.T) {
	got := formatApproval(`{"cluster_id":"BCS-K8S-10001","kind":"StatefulSet","namespace":"default","name":"db","current_replicas":1,"target_replicas":3}`)
	want := "集群 BCS-K8S-10001 的 StatefulSet default/db：副本数 1 → 3"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := formatApproval("unrecognized"); got != "unrecognized" {
		t.Fatalf("fallback = %q", got)
	}
}
