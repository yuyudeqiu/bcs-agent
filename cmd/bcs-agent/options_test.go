package main

import (
	"io"
	"testing"
)

func TestParseOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		prompt  string
		runOnce bool
		wantErr bool
	}{
		{name: "interactive"},
		{name: "short flag", args: []string{"-p", "查看项目列表"}, prompt: "查看项目列表", runOnce: true},
		{name: "long flag", args: []string{"--prompt", "查看集群列表"}, prompt: "查看集群列表", runOnce: true},
		{name: "positional", args: []string{"查看", "项目列表"}, prompt: "查看 项目列表", runOnce: true},
		{name: "mixed", args: []string{"-p", "查看项目", "多余参数"}, wantErr: true},
		{name: "empty flag", args: []string{"-p", "  "}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseOptions(test.args, io.Discard)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseOptions() error = %v", err)
			}
			if err == nil && (got.prompt != test.prompt || got.runOnce != test.runOnce) {
				t.Fatalf("parseOptions() = %+v", got)
			}
		})
	}
}
