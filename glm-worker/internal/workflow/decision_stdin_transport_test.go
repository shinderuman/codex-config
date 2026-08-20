package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDecisionStdinTransportContractWiringはdecision/fix stdin modeの親caller contractを
// glm-execution.md本文の必須契約文で固定する。現行Codex toolではtty:falseのstdin即時EOF、
// tty:trueでもecho有効ならtool outputへの本文複製、canonical input buffering有効なら
// 末尾改行なしchunkのline buffer滞留で輸送が成立しないため、tty・`stty raw -echo`による
// echo-off・canonical/CR-NL置換/signals/flow control無効化・1回write・禁止fallbackの
// 各契約文のいずれかが欠けると失敗する。旧`-icanon -echo`系の弱い固定形の残留も拒否する。
func TestDecisionStdinTransportContractWiring(t *testing.T) {
	root := scenarioRepoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "codex", "instructions", "glm-execution.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)

	wires := []string{
		"exec_commandは`tty: true`で起動する",
		"`tty: false`へのfallbackを禁止する",
		"stty raw -echo && glm-worker --decision-stdin <payload-bytes>",
		"`raw`でinput/output processing・canonical・signals・flow controlをまとめて無効化し、CR/NL置換（ICRNL/INLCR）・制御byteの信号化（ISIG）・flow control（IXON）・stdoutのPTY側加工（OPOST/ONLCR）を防ぐ",
		"`-icanon -echo`だけの弱い設定ではbyte数が同じまま本文が1:1でなく置換され、sha256未指定時にsilent corruptionとなるため使わない",
		"glm-worker起動前にこの`stty`を適用し、`stty`設定が失敗した場合はglm-workerを起動せずfail closedする",
		"command文字列へ入れてよい本文由来の情報はUTF-8 byte長と任意のSHA-256だけに限る",
		"末尾改行の有無に依存せず非emptyの`write_stdin`で本文全体を1回だけ送る",
		"echo有効・`raw`未適用のままの本文送信と、改行だけの追加writeを禁止する",
		"本文の分割再送・短文化・`--decision`/`--fix`へのargv埋込みfallbackを行わない",
		"この固定wrapper command自体はCodex tool側でsandbox外実行する",
		"glm-workerが既存task state/checkpoint/sessionを更新するためである",
		"毎回の再承認要求を本契約へ含めない",
	}
	for _, wire := range wires {
		if !strings.Contains(doc, wire) {
			t.Errorf("glm-execution.md lacks stdin transport wiring: %q", wire)
		}
	}
	// 旧`-icanon -echo min 1 time 0`系の弱い固定形が契約本文へ残留しないことを固定する。
	// sandbox外実行の理由はstate更新権限であり、sttyがsandbox内で成立しない実測と不一致な
	// 文面の再混入も拒否する。
	for _, weak := range []string{"-icanon min 1 time 0", "stty -echo -icanon", "sandbox内へ落ちたshell wrapperでは`stty`による端末制御が成立しない"} {
		if strings.Contains(doc, weak) {
			t.Errorf("glm-execution.md still contains outdated transport contract: %q", weak)
		}
	}
}
