package classify

import (
	"ai-watch/internal/domain"
	"testing"
)

func TestResult(t *testing.T) {
	cases := []struct {
		name             string
		cli              domain.CLI
		code             int
		out              string
		timeout, stopped bool
		want             domain.AttemptStatus
	}{{"success", domain.CLICodex, 0, "READY", false, false, domain.AttemptSuccess}, {"exit zero unmatched", domain.CLICodex, 0, "hello", false, false, domain.AttemptUnmatched}, {"codex fatal", domain.CLICodex, 1, "not logged in", false, false, domain.AttemptFatal}, {"claude overload chinese", domain.CLIClaude, 1, "当前模型负载已达上限，请稍后重试", false, false, domain.AttemptOverloaded}, {"timeout", domain.CLICodex, 124, "", true, false, domain.AttemptTimeout}, {"stopped", domain.CLIClaude, 1, "", false, true, domain.AttemptStopped}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Result(c.cli, c.code, c.out, "READY", c.timeout, c.stopped); got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
}
