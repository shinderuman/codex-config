package runner

import (
	"strconv"
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

// 共通分類入口は5h上限→transient→非一時の順で排他的に判定する。
func TestClassifyProviderFailureTextExclusive(t *testing.T) {
	fiveHour := "API Error: Request rejected (429) · [1308][Usage limit reached for 5 hour. Your limit will reset at 2026-07-22 14:06:34]"
	tests := []struct {
		name       string
		text       string
		wantKind   string
		wantDetail string
	}{
		{"zai 5h limit", fiveHour, ProviderFailureZaiFiveHour, ""},
		// 5h上限本文に503等の別signalが混在しても5h上限が優先する。
		{"zai 5h with mixed 503", fiveHour + " upstream 503", ProviderFailureZaiFiveHour, ""},
		{"http 502", "API Error: 502 Bad Gateway", ProviderFailureTransient, "http-502"},
		{"network dial", "dial tcp: lookup api.z.ai: no such host", ProviderFailureTransient, "network:dial tcp"},
		{"auth 401", "401 Unauthorized", ProviderFailureFatal, ""},
		{"unknown", "boom fatal", ProviderFailureFatal, ""},
		{"empty", "", ProviderFailureFatal, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyProviderFailureText(tt.text)
			if got.Kind != tt.wantKind {
				t.Fatalf("ClassifyProviderFailureText(%q) kind = %q want %q", tt.text, got.Kind, tt.wantKind)
			}
			if tt.wantDetail != "" && got.Detail != tt.wantDetail {
				t.Fatalf("ClassifyProviderFailureText(%q) detail = %q want %q", tt.text, got.Detail, tt.wantDetail)
			}
		})
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

// probe応答内の明示的auth/config信号だけを検出し、sentinel不一致・malformed等の
// semantic invalid(default fatalへ落ちる)や裸の数字・一般語とは区別する。
func TestDetectProbeFatalSignal(t *testing.T) {
	fatal := []string{
		"401 Unauthorized",
		"403 Forbidden",
		"401 Unauthorized: invalid api key",
		"HTTP/1.1 403 Forbidden",
		"400 Bad Request: invalid_request_error",
		"API Error: 401 · invalid_api_key",
		"status code 403 returned",
		"invalid api key",
		"invalid x-api-key provided",
		"API key not valid. Please renew.",
		"authentication failed for this request",
		"authentication required before continuing",
		"permission denied for this credential",
		"invalid model: glm-unknown",
		"model not found: glm-unknown",
	}
	for _, text := range fatal {
		if !DetectProbeFatalSignal(text) {
			t.Fatalf("明示的auth/config信号を検出すべき: %q", text)
		}
	}
	notFatal := []string{
		"",
		"GLM_WORKER_PROBE_OK",
		"Scheduled maintenance is in progress. Please retry later.",
		"probe不正応答(opus): 応答がsentinel \"GLM_WORKER_PROBE_OK\"と一致しません: \"\"",
		"API Error: 503 Service Unavailable",
		"retry failed after waiting 400 ms",
		"queued 403 jobs behind the maintenance window",
		"authentication service unavailable",
		"connecting to localhost:4001 failed",
		"metrics: 4003 requests",
		"unauthorized",
		"Unauthorized",
		"forbidden",
		"Forbidden",
		"This request is unauthorized for the current account",
		"Access to this model is forbidden during maintenance",
		"the caller was unauthorized and forbidden from reading the repository",
	}
	for _, text := range notFatal {
		if DetectProbeFatalSignal(text) {
			t.Fatalf("semantic invalid・裸の数字・一般語をfatalへ誤分類: %q", text)
		}
	}
}

// production matcherの全列挙signalを契約へ照合する。文脈なし一般語や数字だけの登録と、
// 実際には一度も検出されない死にsignalの再登録を防ぐ。代表caseの存在で列挙値の妥当性を
// 代替しない。
func TestProbeFatalSignalsEnumerationMatchesContract(t *testing.T) {
	bareWords := map[string]bool{
		"unauthorized":   true,
		"forbidden":      true,
		"authentication": true,
		"permission":     true,
	}
	for _, signal := range probeFatalSignals {
		if bareWords[signal] {
			t.Fatalf("probeFatalSignalsに文脈なし一般語%qを登録できない", signal)
		}
		if _, err := strconv.Atoi(strings.TrimSpace(signal)); err == nil {
			t.Fatalf("probeFatalSignalsに数字だけのsignal %qを登録できない", signal)
		}
		if !DetectProbeFatalSignal(signal) {
			t.Fatalf("列挙signal %qが実際にはfatal検出されない", signal)
		}
	}
}
