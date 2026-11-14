package main

import (
	"os"
	"testing"
)

func TestIsMCPCommandArgs(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"mcp"}, true},
		{[]string{"serve", "--port", "8080"}, true},
		{[]string{"-v", "mcp"}, true},
		{[]string{"--tier", "fast", "mcp"}, true},
		{[]string{"hook", "session-start"}, true},
		{[]string{"a2a"}, false},
		{[]string{"daemon", "start"}, false},
		{[]string{"status"}, false},
		{[]string{"no-such-command"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := isMCPCommandArgs(c.args); got != c.want {
			t.Errorf("isMCPCommandArgs(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestIsCompletionCommand(t *testing.T) {
	for _, args := range [][]string{{"completion", "zsh"}, {"__complete", "st"}, {"__completeNoDesc", ""}, {"-v", "completion", "bash"}} {
		if !isCompletionCommand(args) {
			t.Errorf("isCompletionCommand(%q) = false", args)
		}
	}
	for _, args := range [][]string{{"status"}, {"mcp"}, nil} {
		if isCompletionCommand(args) {
			t.Errorf("isCompletionCommand(%q) = true", args)
		}
	}
}

func TestIsTerminalRejectsDevNull(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Fatal("/dev/null counted as a terminal: the notice would be recorded as seen without being seen")
	}
}
