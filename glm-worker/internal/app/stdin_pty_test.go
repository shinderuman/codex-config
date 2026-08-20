package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStdinPayloadPTYRawEchoTransportはstdin modeのcaller契約(`stty raw -echo` + tty session)
// が実PTY上で成立することをAI callなしで検証する。末尾改行なしpayload・CR byte・
// Ctrl-C相当byteが宣言byte数どおりproduction readerへ届き、echoされないことを固定する。
// macOSの`script`でPTYを確保するため、darwin以外や`script`不在環境ではskipする。
func TestStdinPayloadPTYRawEchoTransport(t *testing.T) {
	if os.Getenv("GLM_WORKER_STDIN_PTY_HELPER") == "1" {
		stdinPTYHelperMain()
		return
	}
	if runtime.GOOS != "darwin" {
		t.Skip("PTY transport契約の実機検証はmacOSのscript前提")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skipf("script command not found: %v", err)
	}

	payload := "GLMPTYMARK decision 1行目\nCR:\rCtrlC:\x03 quote\"dollar$ `git ls-files -s -z` 日本語"
	outPath := filepath.Join(t.TempDir(), "pty-payload")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// 固定wrapper: 本文はcommandへ入れず、stty raw -echo適用後にtest binary(helper)へexecする。
	cmd := exec.CommandContext(ctx, "script", "-q", "/dev/null", "sh", "-c",
		`stty raw -echo && exec "$GLM_WORKER_STDIN_PTY_BIN" -test.run=TestStdinPayloadPTYRawEchoTransport`)
	cmd.Env = append(os.Environ(),
		"GLM_WORKER_STDIN_PTY_HELPER=1",
		"GLM_WORKER_STDIN_PTY_BIN="+os.Args[0],
		"GLM_WORKER_STDIN_PTY_OUT="+outPath,
		"GLM_WORKER_STDIN_PTY_BYTES="+strconv.Itoa(len(payload)),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, readErr := os.ReadFile(outPath); readErr == nil && string(data) == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("helperがreadyにならない: %q %v", output.String(), cmd.Wait())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 末尾改行なしの本文を1回だけ書き、stdin pipeは開いたまま保つ(EOF不要契約)。
	if _, err := stdin.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("helper終了: %v output=%q", err, output.String())
		}
	case <-ctx.Done():
		t.Fatalf("宣言byte数へ到達せず停止(raw未適用ならcanonical line buffer滞留): output=%q", output.String())
	}

	received, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(received) != payload {
		t.Fatalf("PTY経由payloadが宣言byte数どおり保存されていません: got %q want %q", received, payload)
	}
	if !strings.Contains(string(received), "\r") || !strings.Contains(string(received), "\x03") {
		t.Fatal("CR byteかCtrl-C相当byteが失われています")
	}
	if strings.Contains(output.String(), "GLMPTYMARK") {
		t.Fatalf("本文がechoでtool outputへ複製されています: %q", output.String())
	}
}

// stdinPTYHelperMainは子process側のhelper本体。親と同じtest binaryを起動し、
// ready marker書込み後にproduction readerで宣言byte数だけstdinから読み取る。
func stdinPTYHelperMain() {
	outPath := os.Getenv("GLM_WORKER_STDIN_PTY_OUT")
	want, err := strconv.ParseInt(os.Getenv("GLM_WORKER_STDIN_PTY_BYTES"), 10, 64)
	if err != nil || outPath == "" || want <= 0 {
		os.Exit(2)
	}
	if err := os.WriteFile(outPath, []byte("ready"), 0o600); err != nil {
		os.Exit(2)
	}
	payload, err := readStdinPayload(os.Stdin, want, "")
	if err != nil {
		_ = os.WriteFile(outPath, []byte("ERR: "+err.Error()), 0o600)
		os.Exit(3)
	}
	if err := os.WriteFile(outPath, []byte(payload), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
