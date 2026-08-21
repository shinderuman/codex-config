package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTerminalPayloadSingleRenderContractWiringは親tool orchestrationのterminal payload
// 単一描画contractのproduction wiringと、live positive Eval記録・期待判断との因果を
// 決定論検証する。実運用3回の二面表示を通した上位原因はEVAL.mdのcaller側echo除外と
// 未実行の親behavioral Evalであり、撤回済み除外文の再混入・glm-execution.mdのcaller契約文・
// fixed Eval実在・live実施済み記録のいずれかが欠けると失敗する。repo側dedupeで代替した
// 実装になっていないことと、worker/reviewer promptへ本契約のchecklistを追加しない方針も固定する。
func TestTerminalPayloadSingleRenderContractWiring(t *testing.T) {
	root := scenarioRepoRoot(t)

	readContractFile := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	glmExecution := readContractFile("codex/instructions/glm-execution.md")
	evalDoc := readContractFile("EVAL.md")

	// production caller契約はfunctions.execのorchestration内での変数蓄積・Functionsのstoreへ
	// task固有key保存・captured marker返却・別の短い同期functions.execによるload(key)で構成される。
	for _, wire := range []string{
		"## 親tool orchestrationのterminal payload単一描画",
		"原因層はglm-worker内部emitではなく親Codex orchestrationである",
		"background `functions.exec`の完了outputと後続`functions.wait`のresult cardで同じraw terminal payloadを二面描画する",
		"1 accepted terminal resultにつき、親tool orchestration全体でユーザー可視payloadは1回",
		"repo内emitの再調査・原因境界の特定だけでこの症状を解消扱いしない",
		"nested `exec_command`・`write_stdin`の各outputを変数へ蓄積し",
		"raw stdout・packetをtext・notify・image等の即時描画経路へ一切出さない",
		"Functionsのstoreへtask固有keyで保存し",
		"cellの返り値は短いcaptured marker(`GLM_TERMINAL_CAPTURED <key>`)だけにする",
		"別の短い同期`functions.exec`でstoreのload(key)を読み",
		"text(raw)として1回だけ親へ渡す",
		"この同期callで追加AI call・追加のglm-worker実行を行わない",
		"background cellの完了outputと`functions.wait`の双方へ同じraw payloadを流す運用は禁止する",
		"repo側のPACKET/JSON blind dedupeと正当な別terminal resultの抑止も行わない",
		"structured JSON移行後も結果object全体が同じ境界で二度描画され得る前提を維持し、JSON化を解決根拠にしない",
		"境界検証は追加AI callなしのdelayed markerと実`glm-worker` terminal resultを同じbackground exec→wait→同期取得境界で行う",
		"2026-08-21の本契約適用taskで",
		"同期load後にterminal result全文が1回だけユーザー可視表示されたことを実機確認済みである",
	} {
		if !strings.Contains(glmExecution, wire) {
			t.Errorf("glm-execution.md lacks terminal payload wiring: %q", wire)
		}
	}

	// 実機で採用されたproduction caller手順はorchestration内store/loadであり、shell fileへの
	// redirect・file直接読出しの手順ではない。旧契約文の再混入を拒否する。
	for _, revoked := range []string{
		"> <store>",
		"cat <store>",
		"redirect形",
		"内部store(file)",
	} {
		if strings.Contains(glmExecution, revoked) {
			t.Errorf("glm-execution.md still contains the revoked file-store procedure: %q", revoked)
		}
	}

	// 撤回済みのcaller除外文がEVAL.mdへ再混入していないことを固定する。本除外が3回の
	// 報告を通過した上位原因であるため、文言の部分再掲も許さない。
	for _, revoked := range []string{
		"repo外のため検証対象外",
		"caller側echoの二重表示はrepo外",
	} {
		if strings.Contains(evalDoc, revoked) {
			t.Errorf("EVAL.md still contains the revoked caller exclusion: %q", revoked)
		}
	}

	section := evalTerminalPayloadSection(t, evalDoc)
	// 親behavioral Evalの期待判断(EVAL.md本節)がproduction guidanceのどの契約文へ根拠を
	// 持つかを対で検証する。EVAL.md側の文面だけ、instruction側の契約文だけの片側存在は通さない。
	evalGrounds := []struct {
		eval     string
		guidance string
	}{
		{"1 accepted terminal resultにつき、親tool orchestration全体でユーザー可視payloadは1回」だけとする", "1 accepted terminal resultにつき、親tool orchestration全体でユーザー可視payloadは1回」である"},
		{"repo内emitの再調査・原因境界の特定だけで本項を完了扱いしない", "repo内emitの再調査・原因境界の特定だけでこの症状を解消扱いしない"},
		{"長時間cellではraw stdout・packetをtext・notify・image等の即時描画経路へ出さず内部store(task固有key)へ蓄積し", "nested `exec_command`・`write_stdin`の各outputを変数へ蓄積し"},
		{"長時間cellではraw stdout・packetをtext・notify・image等の即時描画経路へ出さず内部store(task固有key)へ蓄積し", "raw stdout・packetをtext・notify・image等の即時描画経路へ一切出さない"},
		{"長時間cellではraw stdout・packetをtext・notify・image等の即時描画経路へ出さず内部store(task固有key)へ蓄積し", "Functionsのstoreへtask固有keyで保存し"},
		{"cell終端後の短い同期callでのloadで1回だけ親へ渡す", "別の短い同期`functions.exec`でstoreのload(key)を読み"},
		{"同一raw payloadをbackground cellの完了outputと`functions.wait`双方へ流す実装・運用、repo側PACKET/JSON blind dedupe、正当な別terminal resultの抑止", "repo側のPACKET/JSON blind dedupeと正当な別terminal resultの抑止も行わない"},
		{"structured JSON object全体も同じ境界で二度描画され得る前提を維持し、JSON化を解決根拠にしない", "structured JSON移行後も結果object全体が同じ境界で二度描画され得る前提を維持し、JSON化を解決根拠にしない"},
		{"追加AI callなしのdelayed markerと実`glm-worker` binaryのterminal resultを同じbackground exec→wait→同期取得境界で検証する", "境界検証は追加AI callなしのdelayed markerと実`glm-worker` terminal resultを同じbackground exec→wait→同期取得境界で行う"},
		{"raw payloadはlong cellとwait result cardへ表示されず`GLM_TERMINAL_CAPTURED` markerだけが表示され", "raw payloadがlong cellとwaitへ表示されず`GLM_TERMINAL_CAPTURED` markerだけになり"},
	}
	for _, g := range evalGrounds {
		if !strings.Contains(glmExecution, g.guidance) {
			t.Errorf("glm-execution.md lacks guidance grounding %q", g.guidance)
		}
		if !strings.Contains(section, g.eval) {
			t.Errorf("EVAL.md terminal payload section lacks behavioral eval judgment grounded in guidance: %q", g.eval)
		}
	}

	// fixed Eval・live positive Eval・管理文面の参照。撤回・完了証拠・corpus重複禁止・checklist
	// 不追加をEVAL.md本節へ残す。
	for _, wire := range []string{
		"TestTerminalPayloadBoundarySingleRender",
		"internal/app/terminal_payload_boundary_test.go",
		"tool orchestration semanticsをmodel化し",
		"追加AI callなしのdelayed marker",
		"fake claude binary固定応答で追加AI callなしに取得する",
		"TestTerminalPayloadSingleRenderContractWiring",
		"file直接読出し手順の再混入",
		"caller側echoの二重表示をrepo外として検証対象から除外し",
		"完了条件を「ユーザー可視payload 1回」から「repo内emit調査・原因境界特定」へ狭めても機械的に拒否しなかった",
		"caller側の二重描画をrepo外として検証対象から除外し直さない",
		"live positive Evalは2026-08-21に実施済み",
		"`GLM_TERMINAL_CAPTURED` markerだけが表示され",
		"fixed Evalと本live観測を本項の完了証拠とする",
		"親behavioral Evalの代替として`terminal-payload-*`scenarioをcorpusへ追加せず、repo側dedupe実装で代替しない",
		"worker/reviewer promptへ本契約のchecklistを追加しない",
	} {
		if !strings.Contains(section, wire) {
			t.Errorf("EVAL.md terminal payload section lacks eval wiring: %q", wire)
		}
	}

	// 実施済みのlive positive Evalを未実行・保留へ戻す文面の再混入を本節に限って拒否する。
	// 他節の未実行Eval契約とは区別する。
	for _, revoked := range []string{
		"未実行の固定Eval case",
		"ユーザーの明示指示後だけ実行し",
		"本項を未完了のままにする",
	} {
		if strings.Contains(section, revoked) {
			t.Errorf("EVAL.md terminal payload section reverts the executed live eval to pending: %q", revoked)
		}
	}

	// fixed Evalの実在。文面参照だけの自己充足を防ぐため、参照先test関数が実在することを
	// 確認する。
	boundaryTest := readContractFile("glm-worker/internal/app/terminal_payload_boundary_test.go")
	if !strings.Contains(boundaryTest, "func TestTerminalPayloadBoundarySingleRender(") {
		t.Error("EVAL.md references TestTerminalPayloadBoundarySingleRender but the test does not exist")
	}

	// corpusへ本contractの親behavioral Evalを重複するscenarioが追加されていないこと。
	// repo側dedupe・terminal result抑止の実装契約と矛盾するためである。
	sc, _ := loadCorpus(t)
	for _, s := range sc.Scenarios {
		if strings.HasPrefix(s.ID, "terminal-payload-") {
			t.Errorf("scenario %s must not duplicate the parent behavioral eval into the corpus", s.ID)
		}
	}

	// 本contractは親Codex側のcaller手順であり、常時checklistのworker/reviewer prompt
	// 追加で代替した実装になっていないことを固定する。
	for _, promptFile := range []string{"codex/glm-worker/prompts/WORKER.md", "codex/glm-worker/prompts/REVIEWER.md"} {
		prompt := readContractFile(promptFile)
		for _, keyword := range []string{"terminal payload", "単一描画", "二面表示", "内部store", "GLM_TERMINAL_CAPTURED"} {
			if strings.Contains(prompt, keyword) {
				t.Errorf("%s must not add a terminal payload checklist (%s)", promptFile, keyword)
			}
		}
	}
}

func evalTerminalPayloadSection(t *testing.T, evalDoc string) string {
	t.Helper()
	const header = "## 親tool orchestrationのterminal payload単一描画"
	start := strings.Index(evalDoc, header)
	if start < 0 {
		t.Fatalf("EVAL.md lacks section header %q", header)
	}
	rest := evalDoc[start+len(header):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}
