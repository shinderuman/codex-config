package runner

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyTransientFailureHTTPErrors(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"502 gateway", "API Error: 502 Bad Gateway", "http-502"},
		{"503 service", "Request rejected (503)", "http-503"},
		{"504 timeout", "HTTP 504 Gateway Timeout", "http-504"},
		{"529 overloaded", "status code 529", "http-529"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class, transient := ClassifyTransientFailure(tt.text)
			if !transient || class != tt.want {
				t.Fatalf("ClassifyTransientFailure(%q) = (%q, %v), want (%q, true)", tt.text, class, transient, tt.want)
			}
		})
	}
}

func TestClassifyTransientFailureDoesNotMatchPortLikeSubstrings(t *testing.T) {
	class, transient := ClassifyTransientFailure("connecting to localhost:5034 failed")
	if transient {
		t.Fatalf("port-like 5034 must not match: (%q, %v)", class, transient)
	}
}

func TestClassifyTransientFailureNetworkSignals(t *testing.T) {
	for _, text := range []string{
		"dial tcp: lookup api.z.ai: no such host",
		"connection refused",
		"context deadline exceeded",
		"read tcp: unexpected EOF",
	} {
		class, transient := ClassifyTransientFailure(text)
		if !transient || !strings.HasPrefix(class, "network:") {
			t.Fatalf("network信号をtransient扱いすべき: %q -> (%q, %v)", text, class, transient)
		}
	}
}

func TestClassifyTransientFailureRejectsNonTransient(t *testing.T) {
	for _, text := range []string{
		"401 Unauthorized",
		"403 Forbidden",
		"400 Bad Request: invalid_request_error",
		"invalid api key",
		"429 Too Many Requests",
		"API Error: Request rejected (429) · [1308][Usage limit reached for 5 hour. Your limit will reset at 2026-07-22 14:06:34]",
		"session corrupted",
		"",
	} {
		class, transient := ClassifyTransientFailure(text)
		if transient {
			t.Fatalf("非transientを誤検出: %q -> (%q, %v)", text, class, transient)
		}
	}
}

func TestReadTransientSignalMissingFile(t *testing.T) {
	if got := ReadTransientSignal("/nonexistent/transient-signal-xyz"); got != "" {
		t.Fatalf("欠損fileは空文字を期待: %q", got)
	}
}

func TestProviderUnavailableErrorFormat(t *testing.T) {
	err := &ProviderUnavailableError{
		Phase:          "worker-new",
		Classification: "http-503",
		Probes:         4,
		Elapsed:        51 * time.Minute,
		TaskID:         "12345678-aaaa-bbbb-cccc-dddddddddddd",
		RepoRoot:       "/repo",
		RepoShort:      "abcdef123456",
	}
	msg := err.Error()
	for _, want := range []string{
		"STATUS: PROVIDER_UNAVAILABLE",
		"PHASE: worker-new",
		"TASK_ID: 12345678-aaaa-bbbb-cccc-dddddddddddd",
		"REPO_ROOT: /repo",
		"CLASSIFICATION: http-503",
		"PROBES: 4",
		"ELAPSED: 51m0s",
		"RESUME_AVAILABLE: true",
		"RESUME_COMMAND: glm-worker --resume",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ProviderUnavailableErrorに%qがありません:\n%s", want, msg)
		}
	}
	for _, forbidden := range []string{"RATE_LIMITED", "AUTO_RESUME"} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("provider-unavailableは5h上限の%s fieldを含まない: %s", forbidden, msg)
		}
	}
}
