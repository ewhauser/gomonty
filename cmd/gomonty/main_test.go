package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRequiresExplicitPrepareMode(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"prepare"}, {"prepare", "magic"}} {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), args, &stdout, &stderr)
		if err == nil {
			t.Fatalf("run(%v) unexpectedly succeeded", args)
		}
		if stdout.Len() != 0 {
			t.Fatalf("run(%v) wrote stdout before preparation: %q", args, stdout.String())
		}
	}
}

func TestRunRejectsModeSpecificFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"prepare", "download", "--source", "."}, "--source"},
		{[]string{"prepare", "build", "--base-url", "https://example.test"}, "--base-url"},
	}
	for _, test := range tests {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), test.args, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("run(%v) error = %v, want %q", test.args, err, test.want)
		}
	}
}
