package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

func needsSolReviewPacketApp() string {
	return "PACKET_BEGIN\nSTATUS: NEEDS_SOL_REVIEW\nRISK: HIGH\nSUMMARY: review\nREQUIREMENT_COVERAGE: covered\nINVARIANTS: preserved\nTEST_EVIDENCE: ev\nISSUES: i\nRESIDUAL_RISK: r\nTARGETS: t\nARTIFACTS: none\nSOL_QUESTION: q\nPACKET_END\n"
}

func stdinTestPayload() string {
	return "1. 責務/API\n" +
		"- one-shotの`Search(context.Context, repoRoot, query, Options) (Report, error)`を主APIとする。\n" +
		"- `git ls-files -s -z` + `git ls-files --others --exclude-standard -z`でworktree現物を列挙する。\n" +
		"- 展開されてはいけない文字列: $HOME $(id -u) `pwd` \"double\" 'single'\n" +
		"- 日本語・タブ\t・記号<>|&;をそのまま保持する。"
}

func stdinPayloadSHA(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func snapshotStateFiles(t *testing.T, cfg config.AppConfig) map[string]string {
	t.Helper()
	root := filepath.Join(cfg.StateBase, cfg.RepoHash)
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		sum := sha256.Sum256(data)
		snapshot[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func promptSection(t *testing.T, prompt string, start string, end string) string {
	t.Helper()
	i := strings.Index(prompt, start)
	if i < 0 {
		t.Fatalf("promptに%qがありません: %q", start, prompt)
	}
	section := prompt[i+len(start):]
	j := strings.Index(section, end)
	if j < 0 {
		t.Fatalf("promptに%qがありません: %q", end, prompt)
	}
	return section[:j]
}

func prepareWaitingDecisionState(t *testing.T, cfg config.AppConfig) *state.StateStore {
	t.Helper()
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Touch("pending-decision"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingDecision); err != nil {
		t.Fatal(err)
	}
	return st
}

func prepareWaitingSolReviewState(t *testing.T, cfg config.AppConfig) *state.StateStore {
	t.Helper()
	st, err := state.NewStateStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-request", "request"); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("last-review", "review"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskStatus(state.TaskStatusWaitingSolReview); err != nil {
		t.Fatal(err)
	}
	return st
}

func runStdinPayload(t *testing.T, cfg config.AppConfig, r *fakeRunner, args []string, stdin io.Reader) error {
	t.Helper()
	return run(
		args,
		func() (config.AppConfig, error) { return cfg, nil },
		r.factory(),
		stdin,
		io.Discard,
		io.Discard,
	)
}

func assertFailClosedStateUnchanged(t *testing.T, cfg config.AppConfig, before map[string]string, r *fakeRunner, wantStatus state.TaskStatus) {
	t.Helper()
	if len(r.prompts) != 0 {
		t.Fatal("fail closed時にrunnerを起動しています")
	}
	after := snapshotStateFiles(t, cfg)
	if len(after) != len(before) {
		t.Fatalf("fail closed後にstate file構成が変化しました: before=%v after=%v", before, after)
	}
	for name, digest := range before {
		if after[name] != digest {
			t.Fatalf("fail closed後にstate file %s が変化しました", name)
		}
	}
	st := state.AttachStateStore(cfg)
	if st.TaskStatus() != wantStatus {
		t.Fatalf("status = %q, want %q", st.TaskStatus(), wantStatus)
	}
}

func TestRunDecisionStdinPreservesPayloadBytes(t *testing.T) {
	cfg := newAppConfig(t)
	prepareWaitingDecisionState(t, cfg)
	payload := stdinTestPayload()
	r := &fakeRunner{steps: []fakeStep{
		{output: implementedPacketApp("decision applied")},
		{output: needsSolReviewPacketApp()},
	}}

	args := []string{"--decision-stdin", fmt.Sprint(len(payload)), "--sha256", stdinPayloadSHA(payload)}
	if err := runStdinPayload(t, cfg, r, args, strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}

	st := state.AttachStateStore(cfg)
	persisted, err := os.ReadFile(st.Path("last-decision"))
	if err != nil {
		t.Fatal(err)
	}
	if string(persisted) != payload+"\n" {
		t.Fatalf("last-decisionが全byte保存されていません: %q", persisted)
	}
	decision := promptSection(t, r.prompts[0], "\nSOL_DECISION:\n", "\n\n直前の同一タスクの調査文脈を利用し")
	if decision != payload {
		t.Fatalf("worker promptのdecision部分が全byte保存されていません: %q", decision)
	}
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestRunDecisionStdinFailsClosedOnShortRead(t *testing.T) {
	cfg := newAppConfig(t)
	prepareWaitingDecisionState(t, cfg)
	payload := stdinTestPayload()
	r := &fakeRunner{steps: []fakeStep{{output: implementedPacketApp("done")}}}
	before := snapshotStateFiles(t, cfg)

	short := strings.NewReader(payload[:40])
	args := []string{"--decision-stdin", fmt.Sprint(len(payload)), "--sha256", stdinPayloadSHA(payload)}
	err := runStdinPayload(t, cfg, r, args, short)
	if err == nil || !strings.Contains(err.Error(), "stdin payload read failed after 40 of") {
		t.Fatalf("読み取り不足をfail closedする必要があります: %v", err)
	}
	assertFailClosedStateUnchanged(t, cfg, before, r, state.TaskStatusWaitingDecision)
	st := state.AttachStateStore(cfg)
	if !st.Exists("pending-decision") || st.Exists("last-decision") {
		t.Fatalf("waiting decision状態が変わっています: pending=%t last-decision=%t", st.Exists("pending-decision"), st.Exists("last-decision"))
	}
}

func TestRunDecisionStdinFailsClosedOnSHAMismatch(t *testing.T) {
	cfg := newAppConfig(t)
	prepareWaitingDecisionState(t, cfg)
	payload := stdinTestPayload()
	r := &fakeRunner{steps: []fakeStep{{output: implementedPacketApp("done")}}}
	before := snapshotStateFiles(t, cfg)

	other := stdinPayloadSHA(payload + "corrupted")
	args := []string{"--decision-stdin", fmt.Sprint(len(payload)), "--sha256", other}
	err := runStdinPayload(t, cfg, r, args, strings.NewReader(payload))
	if err == nil || !strings.Contains(err.Error(), "stdin payload sha256 mismatch") {
		t.Fatalf("sha256不一致をfail closedする必要があります: %v", err)
	}
	assertFailClosedStateUnchanged(t, cfg, before, r, state.TaskStatusWaitingDecision)
}

func TestRunFixStdinPreservesPayloadBytes(t *testing.T) {
	cfg := newAppConfig(t)
	prepareWaitingSolReviewState(t, cfg)
	payload := stdinTestPayload()
	r := &fakeRunner{steps: []fakeStep{
		{output: implementedPacketApp("fix applied")},
		{output: needsSolReviewPacketApp()},
	}}

	args := []string{"--fix-stdin", fmt.Sprint(len(payload)), "--sha256", stdinPayloadSHA(payload)}
	if err := runStdinPayload(t, cfg, r, args, strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}

	feedback := promptSection(t, r.prompts[0], "\nREVIEW_FEEDBACK:\n", "\n\n同一タスクの既存文脈を利用し")
	if feedback != payload {
		t.Fatalf("worker promptのfix指示部分が全byte保存されていません: %q", feedback)
	}
	st := state.AttachStateStore(cfg)
	if st.TaskStatus() != state.TaskStatusWaitingSolReview {
		t.Fatalf("status = %q", st.TaskStatus())
	}
}

func TestRunFixStdinFailsClosedOnShortRead(t *testing.T) {
	cfg := newAppConfig(t)
	prepareWaitingSolReviewState(t, cfg)
	payload := stdinTestPayload()
	r := &fakeRunner{steps: []fakeStep{{output: implementedPacketApp("done")}}}
	before := snapshotStateFiles(t, cfg)

	short := strings.NewReader(payload[:15])
	args := []string{"--fix-stdin", fmt.Sprint(len(payload))}
	err := runStdinPayload(t, cfg, r, args, short)
	if err == nil || !strings.Contains(err.Error(), "stdin payload read failed after 15 of") {
		t.Fatalf("読み取り不足をfail closedする必要があります: %v", err)
	}
	assertFailClosedStateUnchanged(t, cfg, before, r, state.TaskStatusWaitingSolReview)
	if st := state.AttachStateStore(cfg); !st.Exists("last-review") {
		t.Fatal("last-reviewが失われました")
	}
}

func TestReadStdinPayloadReturnsWithoutEOF(t *testing.T) {
	payload := stdinTestPayload()
	reader, writer := io.Pipe()
	go func() {
		_, _ = writer.Write([]byte(payload))
	}()

	got, err := readStdinPayload(reader, int64(len(payload)), stdinPayloadSHA(payload))
	if err != nil {
		t.Fatal(err)
	}
	if got != payload {
		t.Fatalf("payload = %q", got)
	}
}

func TestReadStdinPayloadAcceptsUppercaseSHA(t *testing.T) {
	payload := stdinTestPayload()
	upper := strings.ToUpper(stdinPayloadSHA(payload))

	got, err := readStdinPayload(strings.NewReader(payload), int64(len(payload)), upper)
	if err != nil {
		t.Fatal(err)
	}
	if got != payload {
		t.Fatalf("payload = %q", got)
	}
}

func TestReadStdinPayloadRejectsInsufficientBytes(t *testing.T) {
	payload := stdinTestPayload()

	_, err := readStdinPayload(strings.NewReader(payload[:10]), int64(len(payload)), "")
	if err == nil || !strings.Contains(err.Error(), "stdin payload read failed after 10 of") {
		t.Fatalf("不足byteをerrorにする必要があります: %v", err)
	}
}
