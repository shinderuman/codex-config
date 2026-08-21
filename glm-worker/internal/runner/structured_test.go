package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// writeStructuredFakeClaudeは結果event本文をstdoutへ出して終わるfake CLIを置く。
// exitCodeはCLIの終了状態を再現する。
func writeStructuredFakeClaude(t *testing.T, resultEvent string, exitCode int) string {
	t.Helper()
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	script := "#!/bin/sh\nprintf '%s\\n' '" + strings.ReplaceAll(resultEvent, "'", "'\\''") + "'\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return commandPath
}

// newStructuredRunnerは引数検証用に呼出ごとの引数を記録するfake CLI runnerを組み立てる。
func newStructuredRunner(t *testing.T, commandPath string) *ClaudeRunner {
	t.Helper()
	promptDir := t.TempDir()
	for _, name := range []string{"WORKER.md", "REVIEWER.md"} {
		if err := os.WriteFile(filepath.Join(promptDir, name), []byte("system"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st := newTestStateStore(t)
	if err := st.Write("task.id", "12345678-aaaa-bbbb-cccc-dddddddddddd"); err != nil {
		t.Fatal(err)
	}
	return NewClaudeRunner(config.AppConfig{
		RepoRoot:        t.TempDir(),
		RepoShort:       "abcdef123456",
		PromptDir:       promptDir,
		ClaudeBin:       commandPath,
		ClaudeConfigDir: filepath.Join(t.TempDir(), "claude-home"),
		EnvAllowlist:    []string{"GLM_ARGS_FILE"},
		WorkerModel:     "worker-model",
		ReviewerModel:   "reviewer-model",
	}, st)
}

// TestClaudeRunnerPassesRoleSchemaは呼出ごとにrole別のtyped schemaが--json-schemaへ
// 渡されることを検証する。schemaはCLI側で構造を強制する契約そのもののため、
// status enumの語彙がroleで切り替わることまで固定する。
func TestClaudeRunnerPassesRoleSchema(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	argumentsPath := filepath.Join(t.TempDir(), "args")
	commandPath := filepath.Join(t.TempDir(), "fake-claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$GLM_ARGS_FILE\"\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"structured_output\":{\"status\":\"IMPLEMENTED\",\"risk\":\"LOW\",\"summary\":\"done\",\"requirement_coverage\":\"covered\",\"tests\":\"pass\",\"unverified\":\"none\"}}'\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLM_ARGS_FILE", argumentsPath)
	r := newStructuredRunner(t, commandPath)

	if _, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "prompt", filepath.Join(t.TempDir(), "worker.log")); err != nil {
		t.Fatal(err)
	}
	workerSchema := schemaFromArguments(t, readLines(t, argumentsPath))
	assertStatusEnum(t, workerSchema, []string{"IMPLEMENTED", "NEEDS_SOL_DECISION"})

	if _, err := r.Run(state.ReviewerRole, "reviewer-1", "reviewer-model", true, "high", "prompt", filepath.Join(t.TempDir(), "reviewer.log")); err != nil {
		t.Fatal(err)
	}
	reviewerSchema := schemaFromArguments(t, readLines(t, argumentsPath))
	assertStatusEnum(t, reviewerSchema, []string{"PASS", "FIX_REQUIRED", "NEEDS_SOL_REVIEW"})
}

func schemaFromArguments(t *testing.T, arguments []string) map[string]any {
	t.Helper()
	value := argumentAfter(arguments, "--json-schema")
	if value == "" {
		t.Fatalf("--json-schemaが引数にありません: %#v", arguments)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(value), &schema); err != nil {
		t.Fatalf("--json-schemaの値がJSONではありません: %v: %q", err, value)
	}
	return schema
}

func assertStatusEnum(t *testing.T, schema map[string]any, want []string) {
	t.Helper()
	properties, _ := schema["properties"].(map[string]any)
	status, _ := properties["status"].(map[string]any)
	rawEnum, _ := status["enum"].([]any)
	var got []string
	for _, item := range rawEnum {
		if text, ok := item.(string); ok {
			got = append(got, text)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("status enum = %#v want %#v", got, want)
	}
	if _, ok := schema["additionalProperties"]; ok {
		t.Fatal("語彙制限に反してadditionalPropertiesへ依存しています")
	}
}

// TestClaudeRunnerStructuredRetryExhaustionFailsClosedはCLIがschema適合出力を
// 上限まで再試行して失敗した終端(subtype=error_max_structured_output_retries)を
// StructuredOutputErrorとして分類することを検証する。
func TestClaudeRunnerStructuredRetryExhaustionFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	commandPath := writeStructuredFakeClaude(t, `{"type":"result","subtype":"error_max_structured_output_retries","is_error":true,"result":null}`, 1)
	r := newStructuredRunner(t, commandPath)

	_, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "prompt", filepath.Join(t.TempDir(), "out.log"))
	if !IsStructuredOutputError(err) {
		t.Fatalf("StructuredOutputErrorを期待: %v", err)
	}
	var structuredErr *StructuredOutputError
	if !errors.As(err, &structuredErr) || structuredErr.Subtype != "error_max_structured_output_retries" {
		t.Fatalf("subtype = %#v err = %v", structuredErr, err)
	}
}

// TestClaudeRunnerSuccessWithoutStructuredOutputFailsClosedは成功result eventに
// structured_outputが無い場合を契約違反としてfail closedすることを検証する。
func TestClaudeRunnerSuccessWithoutStructuredOutputFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtureはUnix系環境向け")
	}

	commandPath := writeStructuredFakeClaude(t, `{"type":"result","subtype":"success","is_error":false,"result":"done"}`, 0)
	r := newStructuredRunner(t, commandPath)

	_, err := r.Run(state.WorkerRole, "worker-new", "worker-model", false, "high", "prompt", filepath.Join(t.TempDir(), "out.log"))
	if !IsStructuredOutputError(err) {
		t.Fatalf("StructuredOutputErrorを期待: %v", err)
	}
	var structuredErr *StructuredOutputError
	if !errors.As(err, &structuredErr) || structuredErr.Subtype != "" {
		t.Fatalf("subtype = %#v err = %v", structuredErr, err)
	}
	if !strings.Contains(err.Error(), "structured_output") {
		t.Fatalf("理由文 = %v", err)
	}
}
