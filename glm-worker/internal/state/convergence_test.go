package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
)

func newConvergenceTestStore(t *testing.T) *StateStore {
	t.Helper()
	st, err := NewStateStore(config.AppConfig{
		StateBase: t.TempDir(),
		RepoHash:  "roundhash",
		RepoRoot:  "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRoundPathClassBucketsDocCodeOther(t *testing.T) {
	cases := map[string]string{
		"docs/README.md":   RoundPathClassDoc,
		"NOTES.txt":        RoundPathClassDoc,
		"LICENSE":          RoundPathClassDoc,
		"CHANGELOG.md":     RoundPathClassDoc,
		"main.go":          RoundPathClassCode,
		"pkg/a.tsx":        RoundPathClassCode,
		"conf/app.toml":    RoundPathClassCode,
		"deploy.sh":        RoundPathClassOther,
		"ci/workflow.yaml": RoundPathClassOther,
		"binary.png":       RoundPathClassOther,
	}
	for path, want := range cases {
		if got := RoundPathClass(path); got != want {
			t.Fatalf("RoundPathClass(%q) = %q want %q", path, got, want)
		}
	}
}

// TestRoundSemanticDigestIgnoresCommentAndWhitespaceOnlyChangesはslash言語で
// comment追記・空白行・行末空白だけの差分を同一意味へ畳め、code行の差分は別digestに
// なることを検証する。
func TestRoundSemanticDigestIgnoresCommentAndWhitespaceOnlyChanges(t *testing.T) {
	before := []byte("package main\n\nfunc main() {\n\tprintln(1)\n}\n")
	after := []byte("package main\n\n// 追加comment\nfunc main() {\n\tprintln(1)  \n}\n\n")
	if RoundSemanticDigest(before, RoundPathClassCode, "main.go") != RoundSemanticDigest(after, RoundPathClassCode, "main.go") {
		t.Fatal("comment・空白だけの差分が意味差分として扱われています")
	}
	semantic := []byte("package main\n\nfunc main() {\n\tprintln(2)\n}\n")
	if RoundSemanticDigest(before, RoundPathClassCode, "main.go") == RoundSemanticDigest(semantic, RoundPathClassCode, "main.go") {
		t.Fatal("code変更が同一意味に畳まれています")
	}
}

// TestRoundSemanticDigestKeepsUnsafeContentUnnormalizedはraw string・directive
// comment・行継続・triple quoteなど正規化が安全と確定できない内容を未正規化扱い
// (空digestまたは別digest)へ倒すことを検証する。
func TestRoundSemanticDigestKeepsUnsafeContentUnnormalized(t *testing.T) {
	if got := RoundSemanticDigest([]byte("s := `raw // not comment`\n"), RoundPathClassCode, "a.go"); got != "" {
		t.Fatalf("backtick含有go fileが正規化されています: %q", got)
	}
	if got := RoundSemanticDigest([]byte("x := 1 + \\\n2\n"), RoundPathClassCode, "a.go"); got != "" {
		t.Fatalf("行継続含有fileが正規化されています: %q", got)
	}
	if got := RoundSemanticDigest([]byte("s = \"\"\"# not comment\"\"\"\n"), RoundPathClassCode, "a.py"); got != "" {
		t.Fatalf("triple quote含有python fileが正規化されています: %q", got)
	}
	// slash言語の言語固有複数行文字列(text block・raw string)内の「//」行も
	// comment除去へ混ぜない。
	for _, unsafe := range []struct {
		path    string
		content string
	}{
		{"A.java", "String s = \"\"\"\n// not comment\n\"\"\";\n"},
		{"A.kt", "val s = \"\"\"\n// not comment\n\"\"\"\n"},
		{"A.swift", "let s = \"\"\"\n// not comment\n\"\"\"\n"},
		{"A.scala", "val s = \"\"\"\n// not comment\n\"\"\"\n"},
		{"A.dart", "var s = '''\n// not comment\n''';\n"},
		{"A.cs", "var s = @\"\n// not comment\n\";\n"},
		{"A.rs", "let s = r#\"\n// not comment\n\"#;\n"},
		{"A.rs", "let s = r##\"\n// not comment\n\"##;\n"},
		{"A.rs", "let s = r###\"\n// not comment\n\"###;\n"},
		{"A.cpp", "auto s = R\"(\n// not comment\n)\";\n"},
		{"A.h", "auto s = R\"(\n// not comment\n)\";\n"},
		{"A.mm", "auto s = R\"(\n// not comment\n)\";\n"},
	} {
		if got := RoundSemanticDigest([]byte(unsafe.content), RoundPathClassCode, unsafe.path); got != "" {
			t.Fatalf("%sの複数行文字列含有fileが正規化されています: %q", unsafe.path, got)
		}
	}

	// text block内容の差分はcomment差分へ誤分類させないため、正規化不能扱い
	// (空digest)同士の比較がsemantic候補へ倒れることをCompareRoundRecordsで確認する。
	javaBefore := &RoundRecord{Paths: []RoundPathState{
		{Path: "A.java", Class: RoundPathClassCode, FullDigest: "j1", SemanticDigest: RoundSemanticDigest([]byte("String s = \"\"\"\nalpha\n\"\"\";\n"), RoundPathClassCode, "A.java")},
	}}
	javaAfter := &RoundRecord{Paths: []RoundPathState{
		{Path: "A.java", Class: RoundPathClassCode, FullDigest: "j2", SemanticDigest: RoundSemanticDigest([]byte("String s = \"\"\"\n// alpha\n\"\"\";\n"), RoundPathClassCode, "A.java")},
	}}
	delta := CompareRoundRecords(javaBefore, javaAfter)
	if delta.Class != RoundDeltaSemantic || delta.SemanticPaths != 1 {
		t.Fatalf("text block内容差分 = %+v want semantic-change", delta)
	}

	if got := RoundSemanticDigest([]byte("run: ./x.sh\n"), RoundPathClassOther, "Makefile"); got != "" {
		t.Fatalf("非対応形式が正規化されています: %q", got)
	}

	// directive commentは意味を持つため除去しない。
	withDirective := []byte("//go:build linux\n\npackage main\n")
	withoutDirective := []byte("package main\n")
	if RoundSemanticDigest(withDirective, RoundPathClassCode, "a.go") == RoundSemanticDigest(withoutDirective, RoundPathClassCode, "a.go") {
		t.Fatal("build directiveが非意味差分として扱われています")
	}

	// doc pathは内容全体が意味を持つため全内容digestと一致する。
	doc := []byte("# 見出し\n本文\n")
	if RoundSemanticDigest(doc, RoundPathClassDoc, "README.md") != roundDigest(doc) {
		t.Fatal("doc pathの意味digestが全内容digestと一致しません")
	}
}

// TestClassifyRoundPathObservesWorktreeは通常file・削除・symlink・repo外pathの
// 観測結果を検証する。
func TestClassifyRoundPathObservesWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("main.go", filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}

	regular := ClassifyRoundPath(root, "main.go")
	if regular.Class != RoundPathClassCode || regular.Deleted || regular.FullDigest == "" || regular.SemanticDigest == "" {
		t.Fatalf("通常file観測 = %+v", regular)
	}
	if got := ClassifyRoundPath(root, "link.go"); got.FullDigest != roundDigest([]byte("main.go")) {
		t.Fatalf("symlink観測 = %+v", got)
	}
	deleted := ClassifyRoundPath(root, "gone.go")
	if !deleted.Deleted || deleted.FullDigest != "" {
		t.Fatalf("削除file観測 = %+v", deleted)
	}
	outside := ClassifyRoundPath(root, "../outside.go")
	if outside.FullDigest != "" || outside.Deleted {
		t.Fatalf("repo外path観測 = %+v", outside)
	}
}

// TestCompareRoundRecordsClassifiesDeltasはround差分分類の主要分岐を検証する。
func TestCompareRoundRecordsClassifiesDeltas(t *testing.T) {
	base := RoundRecord{
		Version: 1, TaskID: "task-1", Seq: 1, ReviewNumber: 1, WorkerPhase: "worker-new",
		CapturedAt: time.Now(),
		Snapshot:   SnapshotDigest{Head: "h", IndexDigest: "i", WorktreeDigest: "w"},
		Paths: []RoundPathState{
			{Path: "main.go", Class: RoundPathClassCode, FullDigest: "f1", SemanticDigest: "s1"},
		},
	}

	if got := CompareRoundRecords(nil, nil); got.Class != RoundDeltaUnknown {
		t.Fatalf("curr無し = %+v", got)
	}
	if got := CompareRoundRecords(nil, &base); got.Class != RoundDeltaInitial {
		t.Fatalf("prev無し = %+v", got)
	}
	baseline := base
	baseline.WorkerPhase = RoundWorkerPhaseBaseline
	if got := CompareRoundRecords(&base, &baseline); got.Class != RoundDeltaBaseline {
		t.Fatalf("baseline自身 = %+v", got)
	}

	same := base
	same.Seq = 2
	same.ReviewNumber = 2
	if got := CompareRoundRecords(&base, &same); got.Class != RoundDeltaSameSnapshot {
		t.Fatalf("同一snapshot = %+v", got)
	}

	// commentのみ差分:意味digest一致・全digest不一致。
	commentOnly := base
	commentOnly.Seq = 2
	commentOnly.Snapshot = SnapshotDigest{Head: "h", IndexDigest: "i", WorktreeDigest: "w2"}
	commentOnly.Paths = []RoundPathState{
		{Path: "main.go", Class: RoundPathClassCode, FullDigest: "f2", SemanticDigest: "s1"},
	}
	if got := CompareRoundRecords(&base, &commentOnly); got.Class != RoundDeltaCommentFormat || got.ChangedPaths != 1 || got.SemanticPaths != 0 || got.DocPaths != 0 {
		t.Fatalf("comment/format差分 = %+v", got)
	}

	// doc追記はcomment/format-onlyへ落ちずdoc-changeとして観測される。
	docAdded := commentOnly
	docAdded.Paths = append(docAdded.Paths, RoundPathState{Path: "README.md", Class: RoundPathClassDoc, FullDigest: "d1"})
	if got := CompareRoundRecords(&base, &docAdded); got.Class != RoundDeltaDocChange || got.ChangedPaths != 2 || got.SemanticPaths != 0 || got.DocPaths != 1 {
		t.Fatalf("doc追記 = %+v", got)
	}

	// 意味差分:意味digest不一致。
	semantic := commentOnly
	semantic.Paths = []RoundPathState{
		{Path: "main.go", Class: RoundPathClassCode, FullDigest: "f3", SemanticDigest: "s2"},
	}
	if got := CompareRoundRecords(&base, &semantic); got.Class != RoundDeltaSemantic || got.SemanticPaths != 1 {
		t.Fatalf("意味差分 = %+v", got)
	}

	// 正規化不能言語の変更はsemantic候補。
	shellChanged := commentOnly
	shellChanged.Paths = append(shellChanged.Paths, RoundPathState{Path: "run.sh", Class: RoundPathClassOther, FullDigest: "sh2"})
	if got := CompareRoundRecords(&base, &shellChanged); got.Class != RoundDeltaSemantic {
		t.Fatalf("非対応形式変更 = %+v", got)
	}

	// code path削除(取り消し)はsemantic候補。
	reverted := commentOnly
	reverted.Paths = nil
	if got := CompareRoundRecords(&base, &reverted); got.Class != RoundDeltaSemantic || got.ChangedPaths != 1 || got.SemanticPaths != 1 {
		t.Fatalf("取り消し = %+v", got)
	}

	// 観測失敗はunknown。
	captureFailed := commentOnly
	captureFailed.CaptureError = "collect failed"
	if got := CompareRoundRecords(&base, &captureFailed); got.Class != RoundDeltaUnknown {
		t.Fatalf("観測失敗 = %+v", got)
	}

	// snapshotが変わったがpath差分が無い(観測範囲外変更)はunknown。
	outside := commentOnly
	outside.Paths = base.Paths
	if got := CompareRoundRecords(&base, &outside); got.Class != RoundDeltaUnknown {
		t.Fatalf("範囲外変更 = %+v", got)
	}
}

// TestCompareRoundRecordsDocChangesGetOwnClassは文書pathの追加・変更・削除がfile種別
// だけで非意味へ分類されずdoc-changeとして観測されること、AGENTS・instructions・
// EVAL・仕様等の行動規定文書がcomment/format-onlyへ落ちないこと、実codeの
// comment/format-only分類が維持されることを検証する。
func TestCompareRoundRecordsDocChangesGetOwnClass(t *testing.T) {
	prev := RoundRecord{
		Version: 1, TaskID: "task-doc", Seq: 1, ReviewNumber: 1, WorkerPhase: "worker-new",
		Snapshot: SnapshotDigest{Head: "h", IndexDigest: "i", WorktreeDigest: "w"},
		Paths: []RoundPathState{
			{Path: "AGENTS.md", Class: RoundPathClassDoc, FullDigest: "ad1", SemanticDigest: "ad1"},
			{Path: "EVAL.md", Class: RoundPathClassDoc, FullDigest: "ed1", SemanticDigest: "ed1"},
			{Path: "codex/instructions/worker/go.md", Class: RoundPathClassDoc, FullDigest: "id1", SemanticDigest: "id1"},
			{Path: "SPECIFICATION.md", Class: RoundPathClassDoc, FullDigest: "sd1", SemanticDigest: "sd1"},
			{Path: "NOTES.txt", Class: RoundPathClassDoc, FullDigest: "nd1", SemanticDigest: "nd1"},
			{Path: "main.go", Class: RoundPathClassCode, FullDigest: "f1", SemanticDigest: "s1"},
			{Path: "util.go", Class: RoundPathClassCode, FullDigest: "u1", SemanticDigest: "t1"},
		},
	}
	entries := roundPathIndex(prev.Paths)
	pick := func(path string) RoundPathState {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("基準recordに%qがありません", path)
		}
		return entry
	}
	// withDigestは全内容と意味digestが両方変わる差分(doc・code意味差分)。
	withDigest := func(path string, digest string) RoundPathState {
		entry := pick(path)
		entry.FullDigest = digest
		entry.SemanticDigest = digest
		return entry
	}
	// withFullDigestは全内容だけが変わる差分(code comment/format-only)。
	withFullDigest := func(path string, digest string) RoundPathState {
		entry := pick(path)
		entry.FullDigest = digest
		return entry
	}
	// replaceは基準path集合の同path entryを差し替える。
	replace := func(mutations ...RoundPathState) []RoundPathState {
		list := append([]RoundPathState(nil), prev.Paths...)
		for _, mutation := range mutations {
			for i := range list {
				if list[i].Path == mutation.Path {
					list[i] = mutation
				}
			}
		}
		return list
	}
	// dropは基準path集合からpathを除外する(record上の削除)。
	drop := func(path string) []RoundPathState {
		list := make([]RoundPathState, 0, len(prev.Paths)-1)
		for _, entry := range prev.Paths {
			if entry.Path != path {
				list = append(list, entry)
			}
		}
		return list
	}
	curr := func(paths ...RoundPathState) RoundRecord {
		record := prev
		record.Seq = 2
		record.ReviewNumber = 2
		record.Snapshot = SnapshotDigest{Head: "h", IndexDigest: "i", WorktreeDigest: "w2"}
		record.Paths = paths
		return record
	}
	scenarios := []struct {
		name     string
		curr     RoundRecord
		class    string
		changed  int
		semantic int
		doc      int
	}{
		{
			name:  "行動規定文書の変更",
			curr:  curr(replace(withDigest("AGENTS.md", "ad2"))...),
			class: RoundDeltaDocChange, changed: 1, semantic: 0, doc: 1,
		},
		{
			name:  "instructions変更とEVAL変更の同居",
			curr:  curr(replace(withDigest("EVAL.md", "ed2"), withDigest("codex/instructions/worker/go.md", "id2"))...),
			class: RoundDeltaDocChange, changed: 2, semantic: 0, doc: 2,
		},
		{
			name:  "doc追加",
			curr:  curr(append(replace(), RoundPathState{Path: "docs/README.md", Class: RoundPathClassDoc, FullDigest: "d1", SemanticDigest: "d1"})...),
			class: RoundDeltaDocChange, changed: 1, semantic: 0, doc: 1,
		},
		{
			name:  "doc削除",
			curr:  curr(drop("SPECIFICATION.md")...),
			class: RoundDeltaDocChange, changed: 1, semantic: 0, doc: 1,
		},
		{
			name:  "worktree上で削除されたdoc",
			curr:  curr(replace(RoundPathState{Path: "NOTES.txt", Class: RoundPathClassDoc, Deleted: true})...),
			class: RoundDeltaDocChange, changed: 1, semantic: 0, doc: 1,
		},
		{
			name:  "doc変更とcode comment-onlyの同居",
			curr:  curr(replace(withDigest("AGENTS.md", "ad2"), withFullDigest("main.go", "f2"))...),
			class: RoundDeltaDocChange, changed: 2, semantic: 0, doc: 1,
		},
		{
			name:  "doc変更とcode意味差分の同居",
			curr:  curr(replace(withDigest("AGENTS.md", "ad2"), withDigest("util.go", "u2"))...),
			class: RoundDeltaSemantic, changed: 2, semantic: 1, doc: 1,
		},
		{
			name:  "code comment-only単独は維持",
			curr:  curr(replace(withFullDigest("main.go", "f2"))...),
			class: RoundDeltaCommentFormat, changed: 1, semantic: 0, doc: 0,
		},
		{
			name:  "docからcode種別への遷移",
			curr:  curr(replace(RoundPathState{Path: "NOTES.txt", Class: RoundPathClassCode, FullDigest: "n2", SemanticDigest: "n2"})...),
			class: RoundDeltaDocChange, changed: 1, semantic: 0, doc: 1,
		},
	}
	for _, scenario := range scenarios {
		delta := CompareRoundRecords(&prev, &scenario.curr)
		if delta.Class != scenario.class || delta.ChangedPaths != scenario.changed ||
			delta.SemanticPaths != scenario.semantic || delta.DocPaths != scenario.doc {
			t.Fatalf("%s = %+v want class=%s changed=%d semantic=%d doc=%d", scenario.name, delta, scenario.class, scenario.changed, scenario.semantic, scenario.doc)
		}
	}
}

// TestAppendAndReadRoundRecordsはseq採番・roundtrip・破損行と旧version行の
// 扱いを検証する。
func TestAppendAndReadRoundRecords(t *testing.T) {
	st := newConvergenceTestStore(t)
	first := RoundRecord{TaskID: "task-1", ReviewNumber: 1, WorkerPhase: "worker-new", CapturedAt: time.Now()}
	if err := st.AppendRoundRecord(first); err != nil {
		t.Fatal(err)
	}
	second := RoundRecord{TaskID: "task-1", ReviewNumber: 2, WorkerPhase: "worker-auto-fix-1", CapturedAt: time.Now()}
	if err := st.AppendRoundRecord(second); err != nil {
		t.Fatal(err)
	}

	records, err := st.ReadRoundRecords("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("record数 = %d want 2", len(records))
	}
	if records[0].Version != roundLogVersion || records[0].Seq != 1 || records[1].Seq != 2 {
		t.Fatalf("version/seq採番が不正: %+v %+v", records[0], records[1])
	}
	if records[1].ReviewNumber != 2 || records[1].WorkerPhase != "worker-auto-fix-1" {
		t.Fatalf("roundtrip内容が不正: %+v", records[1])
	}

	if _, err := ParseRoundLine([]byte("{\"version\":1,\"kind\":\"brokencorrupt")); err == nil {
		t.Fatal("破損行が読めています")
	}
	if _, err := ParseRoundLine([]byte("{\"version\":99,\"task_id\":\"t\"}")); err == nil {
		t.Fatal("旧version行が読めています")
	}

	if _, err := st.ReadRoundRecords("task-none"); !os.IsNotExist(err) {
		t.Fatalf("不在log読み取り = %v", err)
	}
}

// TestAppendRoundRecordFailureIsolationは追記失敗がerrorとして返ることと、
// log pathがdirectoryで塞がれた場合の失敗が呼出元へ伝わることを検証する。
func TestAppendRoundRecordFailureIsolation(t *testing.T) {
	st := newConvergenceTestStore(t)
	if err := os.MkdirAll(st.RoundLogPath("task-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendRoundRecord(RoundRecord{TaskID: "task-1"}); err == nil {
		t.Fatal("追記失敗が無視されています")
	}
}
