package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLICommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		prompt  string
		runOnce bool
		wantErr string
	}{
		{name: "interactive", args: []string{"cli"}},
		{name: "short flag", args: []string{"cli", "-p", "查看项目列表"}, prompt: "查看项目列表", runOnce: true},
		{name: "long flag", args: []string{"cli", "--prompt", "查看集群列表"}, prompt: "查看集群列表", runOnce: true},
		{name: "positional", args: []string{"cli", "查看", "项目列表"}, prompt: "查看 项目列表", runOnce: true},
		{name: "mixed", args: []string{"cli", "-p", "查看项目", "多余参数"}, wantErr: "不能同时使用"},
		{name: "empty flag", args: []string{"cli", "-p", "  "}, wantErr: "prompt 不能为空"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			var got cliOptions
			command := newRootCommand(commandRunner{
				cli: func(_ context.Context, options cliOptions) error {
					called = true
					got = options
					return nil
				},
				server: func(context.Context) error { return nil },
			}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			command.SetArgs(test.args)

			err := command.Execute()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Execute() error = %v, want containing %q", err, test.wantErr)
				}
				if called {
					t.Fatal("CLI runner called for invalid arguments")
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !called {
				t.Fatal("CLI runner was not called")
			}
			if got != (cliOptions{prompt: test.prompt, runOnce: test.runOnce}) {
				t.Fatalf("CLI options = %+v", got)
			}
		})
	}
}

func TestServerCommand(t *testing.T) {
	called := false
	command := newRootCommand(commandRunner{
		cli: func(context.Context, cliOptions) error { return nil },
		server: func(context.Context) error {
			called = true
			return nil
		},
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	command.SetArgs([]string{"server"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("server runner was not called")
	}
}

func TestRootCommandShowsHelp(t *testing.T) {
	called := false
	output := &bytes.Buffer{}
	command := newRootCommand(commandRunner{
		cli: func(context.Context, cliOptions) error {
			called = true
			return nil
		},
		server: func(context.Context) error {
			called = true
			return nil
		},
	}, strings.NewReader(""), output, &bytes.Buffer{})
	command.SetArgs(nil)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if called {
		t.Fatal("runner called without a subcommand")
	}
	if !strings.Contains(output.String(), "Available Commands") {
		t.Fatalf("help output = %q", output.String())
	}
}
