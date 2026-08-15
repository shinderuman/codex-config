package runner

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// transientHTTPPatternは応答本文中の過渡HTTP statusを一致させる。
// 単語境界で挟みポート番号等の部分一致を避ける。
var transientHTTPPattern = regexp.MustCompile(`\b(502|503|504|529)\b`)

// transientNetworkSignalsは明確な一時ネットワーク障害の文字列信号。
// 汎用“timeout”や“EOF”単体は誤一致が大きいため使わず、具現形だけを列挙する。
var transientNetworkSignals = []string{
	"connection refused",
	"connection reset",
	"i/o timeout",
	"context deadline exceeded",
	"dial tcp",
	"no such host",
	"network is unreachable",
	"transport is closing",
	"unexpected eof",
	"temporary failure",
	"server closed idle connection",
	"proxyconnect",
}

// probeFatalHTTPPatternはprobe応答本文中の認証・要求不正のHTTP statusを文脈付きで一致させる。
// 裸の数字だけでは番号・容量等の誤検出が大きいため、HTTP/status/error/API error等の文脈が
// 直近にあるか、401 Unauthorized/403 Forbidden/400 Bad Requestの組合せのときだけ一致させる。
var probeFatalHTTPPattern = regexp.MustCompile(`(?i)\b(?:http|status|error|api)[^\n]{0,24}\b(?:400|401|403)\b|\b(?:400|401|403)\b[^\n]{0,24}\b(?:bad request|unauthorized|forbidden)\b`)

// probeFatalSignalsはretry不能なauth/config障害の明示的な文字列信号。
// authentication単独等の一般語は文脈で容易に誤検出するため、明示表現だけを列挙する。
var probeFatalSignals = []string{
	"unauthorized",
	"forbidden",
	"invalid api key",
	"invalid_api_key",
	"invalid x-api-key",
	"api key not valid",
	"authentication failed",
	"authentication required",
	"permission denied",
	"invalid model",
	"model not found",
}

// DetectProbeFatalSignalはprobe応答本文中の明示的なauth/config系非retry信号だけを検出する。
// exit 0 + is_errorやsentinel不一致で返った応答でもこれらの信号を含む場合はsemantic invalid
// (probe-contract)としてbackoffせず、既存classifierと同じfatal経路へfail closedさせる。
// 呼出し側は5h上限→transientの分類を先に適用し、この検出はその後に使う。
func DetectProbeFatalSignal(text string) bool {
	if probeFatalHTTPPattern.MatchString(text) {
		return true
	}
	lower := strings.ToLower(text)
	for _, signal := range probeFatalSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

// ClassifyTransientFailureはClaude CLIの出力本文からZ.ai 5h上限以外の一時障害を分類する。
// 502/503/504/529のHTTP statusか明確な一時ネットワーク障害のときだけtransient=trueを返す。
// auth(401/403)・invalid request(400)・generic 429・session破損・不明errorはtransient扱いせず、
// 呼出し元で従来どおりWORKER_ERRORへ分類させる。5h上限はこの関数の呼出し前に別経路で処理するため
// ここへ流入しないが、5h文字列(429/1308/Usage limit reached)はいずれの信号にも一致しない。
func ClassifyTransientFailure(text string) (classification string, transient bool) {
	if match := transientHTTPPattern.FindString(text); match != "" {
		return "http-" + match, true
	}
	for _, signal := range transientNetworkSignals {
		if strings.Contains(strings.ToLower(text), signal) {
			return "network:" + signal, true
		}
	}
	return "", false
}

// ReadTransientSignalは出力fileから分類用の本文を読む。読めなければ空文字を返す。
func ReadTransientSignal(outputPath string) string {
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return ""
	}
	return string(data)
}

// ProviderFailureClassはKindが常に1つの停止理由だけを表す排他的な分類結果。
type ProviderFailureClass struct {
	Kind          string
	Detail        string
	FiveHourLimit ZaiFiveHourLimit
}

const (
	ProviderFailureZaiFiveHour = "zai-5h"
	ProviderFailureTransient   = "transient"
	ProviderFailureFatal       = "fatal"
	// ProbeContractFailureはprobe応答の契約違反(is_error・sentinel不一致・malformed)の分類。
	// 明示的なauth/config信号(DetectProbeFatalSignal)を含まない限り即fatalにせずtransientな
	// probe失敗と同じbackoff/retry対象で、回復しないまま上限・deadline到達時のprovider-unavailable
	// 停止分類として応答契約違反を区別する。
	ProbeContractFailure = "probe-contract"
)

// ClassifyProviderFailureTextは出力本文を5h上限→transient→fatalの順で排他的に分類する。
func ClassifyProviderFailureText(text string) ProviderFailureClass {
	if limit, ok := DetectZaiFiveHourLimitText(text); ok {
		return ProviderFailureClass{Kind: ProviderFailureZaiFiveHour, FiveHourLimit: limit}
	}
	if classification, transient := ClassifyTransientFailure(text); transient {
		return ProviderFailureClass{Kind: ProviderFailureTransient, Detail: classification}
	}
	return ProviderFailureClass{Kind: ProviderFailureFatal}
}

// ProviderUnavailableErrorは一時障害回復がprobe上限・deadlineに到達し、
// WORKER_ERRORやRATE_LIMITEDとは独立した再開可能な停止状態へ移行したことを表す。
// 5h上限のようなCodex heartbeat自動wakeは設定せず、利用者が--resumeで再開する。
type ProviderUnavailableError struct {
	Phase          string
	Classification string
	Probes         int
	Elapsed        time.Duration
	TaskID         string
	RepoRoot       string
	RepoShort      string
}

func (e *ProviderUnavailableError) Error() string {
	return fmt.Sprintf(
		"STATUS: PROVIDER_UNAVAILABLE\nPHASE: %s\nTASK_ID: %s\nREPO_ROOT: %s\nCLASSIFICATION: %s\nPROBES: %d\nELAPSED: %s\nRESUME_AVAILABLE: true\nRESUME_COMMAND: glm-worker --resume",
		valueOrUnknown(e.Phase),
		valueOrUnknown(e.TaskID),
		valueOrUnknown(e.RepoRoot),
		valueOrUnknown(e.Classification),
		e.Probes,
		e.Elapsed.Truncate(time.Second),
	)
}
