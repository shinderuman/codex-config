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
