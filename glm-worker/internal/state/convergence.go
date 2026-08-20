package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// roundLogVersionはRoundRecord JSONのschema version。TaskEventRecordと同じ規則で、
// 既存fieldの意味やJSON名を変更するときだけbumpし、新fieldのomitempty追加はbump不要。
const roundLogVersion = 1

// round記録のpath分類。docは文書file、codeは行単位正規化で意味比較できる言語、
// otherは機械的意味比較を適用しない対象(any change = semantic候補)。
const (
	RoundPathClassDoc   = "doc"
	RoundPathClassCode  = "code"
	RoundPathClassOther = "other"
)

// RoundDeltaClassはround間の状態差分の機械分類。doc-change・semantic-change・
// unknownは省略候補から除外される安全側。verification-onlyはsame-snapshotをevent logの
// tool観測で細分化したapp層の派生値で、ここではsame-snapshotまでを決める。
const (
	RoundDeltaBaseline      = "baseline"
	RoundDeltaInitial       = "initial"
	RoundDeltaSameSnapshot  = "same-snapshot"
	RoundDeltaCommentFormat = "comment-format-only"
	RoundDeltaDocChange     = "doc-change"
	RoundDeltaSemantic      = "semantic-change"
	RoundDeltaUnknown       = "unknown"
)

// RoundWorkerPhaseBaselineはtask開始時の境界record(worker実行前)を表す予約phase。
const RoundWorkerPhaseBaseline = "baseline"

// RoundPathStateはround境界的な変更対象path 1件の観測。FullDigestはworktree内容の
// sha256(生内容は保存しない)。SemanticDigestは空白行・full-line comment・行末空白を
// 除去した正規化内容のsha256で、言語的に正規化できない場合は空文字(未決定)。
// Deletedは変更対象setに入ったpathがworktreeへ存在しない状態。
type RoundPathState struct {
	Path           string `json:"path"`
	Class          string `json:"class"`
	Deleted        bool   `json:"deleted,omitempty"`
	FullDigest     string `json:"full_digest,omitempty"`
	SemanticDigest string `json:"semantic_digest,omitempty"`
}

// RoundRecordはreview round 1件の開始境界(worker終了時)またはtask開始時baselineの
// repo状態観測。Reviewer呼出の結果・token・durationはtelemetryが正なのでここへ持たず、
// 表示側で時間窓とphaseで対応付ける。CaptureErrorはpath分類の失敗理由で、snapshot
// digest自体は別に記録される。
type RoundRecord struct {
	Version      int              `json:"version"`
	TaskID       string           `json:"task_id"`
	Seq          int              `json:"seq"`
	ReviewNumber int              `json:"review_number"`
	AutoFixes    int              `json:"auto_fixes"`
	WorkerPhase  string           `json:"worker_phase"`
	CapturedAt   time.Time        `json:"captured_at"`
	Snapshot     SnapshotDigest   `json:"snapshot"`
	Paths        []RoundPathState `json:"paths,omitempty"`
	CaptureError string           `json:"capture_error,omitempty"`
}

func (s *StateStore) RoundLogPath(taskID string) string {
	return s.Path(filepath.Join("rounds", taskID+".jsonl"))
}

// AppendRoundRecordはround記録をtask単位logへ追記する。seqはlog内の既存最大seq+1で
// 採番する(追記自体が失敗したrecordは残らないため、読み側ではtelemetryのreviewer番号
// との突合でrecord欠落を検出する)。観測資料のため失敗はwarningだけ出し呼出元のtask
// 成否へ影響させない。
func (s *StateStore) AppendRoundRecord(record RoundRecord) error {
	if record.Version == 0 {
		record.Version = roundLogVersion
	}
	seq := 1
	records, err := s.ReadRoundRecords(record.TaskID)
	if err == nil && len(records) > 0 {
		seq = records[len(records)-1].Seq + 1
	}
	record.Seq = seq
	data, err := json.Marshal(record)
	if err != nil {
		warnRoundRecordFailure("JSON化", err)
		return err
	}
	path := s.RoundLogPath(record.TaskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		warnRoundRecordFailure("log dir作成", err)
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		warnRoundRecordFailure("log open", err)
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		warnRoundRecordFailure("log chmod", err)
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		warnRoundRecordFailure("追記", err)
		return err
	}
	return file.Close()
}

// ParseRoundLineはround log 1行をdecodeする。破損行・旧version recordはerrorとなり、
// 呼出元がその行だけをskipできる。
func ParseRoundLine(data []byte) (RoundRecord, error) {
	var record RoundRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return RoundRecord{}, fmt.Errorf("round recordを読めません: %w", err)
	}
	if record.Version != roundLogVersion {
		return RoundRecord{}, fmt.Errorf("unsupported round record version: %d", record.Version)
	}
	return record, nil
}

func (s *StateStore) ReadRoundRecords(taskID string) ([]RoundRecord, error) {
	data, err := os.ReadFile(s.RoundLogPath(taskID))
	if err != nil {
		return nil, err
	}
	var records []RoundRecord
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		record, err := ParseRoundLine([]byte(line))
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func warnRoundRecordFailure(operation string, err error) {
	fmt.Fprintf(statsWarnOut, "WARNING: round記録の%sに失敗しました（観測用のためtask本体へ影響しません）: %v\n", operation, err)
}

// ClassifyRoundPathsは変更対象path集合をworktree観測へ変換する。path順で安定させ、
// 1件の読み取り失敗は当該pathのdigest欠落(=semantic候補)へ倒し全体は失敗させない。
func ClassifyRoundPaths(repoRoot string, paths []string) []RoundPathState {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	result := make([]RoundPathState, 0, len(sorted))
	for _, path := range sorted {
		result = append(result, ClassifyRoundPath(repoRoot, path))
	}
	return result
}

// ClassifyRoundPathはworktree上の1 pathの内容観測を返す。repo境界越え・取得失敗は
// digest空文字(未観測)として返し、errorにはしない(観測はbest-effortで、欠落は
// 分類側へ安全側へ解釈させる)。
func ClassifyRoundPath(repoRoot string, relPath string) RoundPathState {
	entry := RoundPathState{Path: relPath, Class: RoundPathClass(relPath)}
	absPath, err := joinWithinRoot(repoRoot, relPath)
	if err != nil {
		return entry
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			entry.Deleted = true
		}
		return entry
	}
	switch {
	case info.Mode().IsRegular():
		content, err := os.ReadFile(absPath)
		if err != nil {
			return entry
		}
		entry.FullDigest = roundDigest(content)
		entry.SemanticDigest = RoundSemanticDigest(content, entry.Class, relPath)
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(absPath)
		if err != nil {
			return entry
		}
		entry.FullDigest = roundDigest([]byte(target))
	}
	return entry
}

// RoundPathClassは拡張子・basenameからpath分類を返す。文書は言語正規化を適用しない
// 別扱い(差分はdoc-changeへ観測)とし、既知の言語だけcode、それ以外(shell・yaml等の
// 複数行文字列を除外できない形式)はother。
func RoundPathClass(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".rst", ".adoc", ".txt":
		return RoundPathClassDoc
	case ".go", ".c", ".h", ".cpp", ".hpp", ".cc", ".hh", ".cxx", ".hxx",
		".java", ".kt", ".kts", ".cs", ".swift", ".rs", ".dart", ".scala",
		".m", ".mm", ".php",
		".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs",
		".py", ".toml", ".ini", ".cfg",
		".json", ".css", ".scss", ".less":
		return RoundPathClassCode
	}
	switch filepath.Base(path) {
	case "LICENSE", "NOTICE", "CHANGELOG", "CHANGELOG.md":
		return RoundPathClassDoc
	}
	return RoundPathClassOther
}

// roundCommentKindはcomment行の除去規則。slashは「//」、hashは「#」。
const (
	roundCommentSlash = "slash"
	roundCommentHash  = "hash"
	roundCommentNone  = "none"
)

func roundCommentKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".c", ".h", ".cpp", ".hpp", ".cc", ".hh", ".cxx", ".hxx",
		".java", ".kt", ".kts", ".cs", ".swift", ".rs", ".dart", ".scala",
		".m", ".mm", ".php",
		".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return roundCommentSlash
	case ".py", ".toml", ".ini", ".cfg":
		return roundCommentHash
	}
	return roundCommentNone
}

// RoundSemanticDigestは内容の意味比較用digestを返す。正規化が言語的に安全と
// 確定できない場合は空文字を返し、呼出元をsemantic候補へ倒させる。正規化は
// 空白行・行末空白・full-line commentの除去だけにとどめ、構造 parseは行わない。
// 複数行文字列(raw string・triple quote・heredoc・行継続)が存在しうる内容は
// 安全に確定できないため全て正規化対象から除外する。
func RoundSemanticDigest(content []byte, class string, path string) string {
	if len(content) == 0 {
		return roundDigest(content)
	}
	if class == RoundPathClassDoc {
		return roundDigest(content)
	}
	if class != RoundPathClassCode {
		return ""
	}
	if bytes.Contains(content, []byte("\\\n")) {
		return ""
	}
	kind := roundCommentKind(path)
	switch kind {
	case roundCommentSlash:
		if bytes.IndexByte(content, '`') >= 0 {
			return ""
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".php" && bytes.Contains(content, []byte("<<<")) {
			return ""
		}
		for _, marker := range roundSlashStringGuardMarkers(ext) {
			if bytes.Contains(content, marker) {
				return ""
			}
		}
	case roundCommentHash:
		if bytes.Contains(content, []byte(`"""`)) || bytes.Contains(content, []byte(`'''`)) {
			return ""
		}
	}
	return roundDigest(normalizeRoundContent(content, kind))
}

// roundSlashStringGuardMarkersはslash comment言語のうち言語固有の複数行文字列を
// 持つ形式のguard marker。text block(Java・Kotlin・Scala・Swift・Dart・C#)や
// raw string(Rust・C++)の内部行は「//」で始っていても文字列内容のため、
// 開始列を含むfileは正規化対象から除外する。
func roundSlashStringGuardMarkers(ext string) [][]byte {
	switch ext {
	case ".java", ".kt", ".kts", ".scala":
		return [][]byte{[]byte(`"""`)}
	case ".swift", ".dart":
		return [][]byte{[]byte(`"""`), []byte(`'''`)}
	case ".cs":
		return [][]byte{[]byte(`"""`), []byte(`@"`)}
	case ".rs":
		return [][]byte{[]byte(`r"`), []byte(`#"`)}
	case ".cpp", ".hpp", ".cc", ".hh", ".cxx", ".hxx", ".h", ".mm":
		return [][]byte{[]byte(`R"`)}
	}
	return nil
}

// normalizeRoundContentは空白行・行末空白(改行種含む)・full-line comment行を除去する。
// comment除去はdirective形式(「//go:build」等の直後に識別子とcolonが続く行)・
// shebang・encoding宣言を残し、意味を持つ可能性がある行を誤って落とさない。
func normalizeRoundContent(content []byte, kind string) []byte {
	var kept [][]byte
	for _, line := range bytes.Split(content, []byte("\n")) {
		trimmed := bytes.TrimRight(line, " \t\r")
		if len(trimmed) == 0 {
			continue
		}
		if kind != roundCommentNone && roundCommentLine(trimmed, kind) {
			continue
		}
		kept = append(kept, trimmed)
	}
	return bytes.Join(kept, []byte("\n"))
}

func roundCommentLine(line []byte, kind string) bool {
	trimmed := bytes.TrimLeft(line, " \t")
	var marker []byte
	switch kind {
	case roundCommentSlash:
		marker = []byte("//")
	case roundCommentHash:
		marker = []byte("#")
	default:
		return false
	}
	if !bytes.HasPrefix(trimmed, marker) {
		return false
	}
	rest := trimmed[len(marker):]
	if kind == roundCommentHash {
		if bytes.HasPrefix(rest, []byte("!")) || bytes.HasPrefix(rest, []byte("-*-")) {
			return false
		}
	}
	if roundDirectiveComment(rest) {
		return false
	}
	return true
}

// roundDirectiveCommentは「go:build」「noqa:」「sourceURL:」等、comment内でも
// tooling意味を持つ「識別子:」形式を検出する。tooling directiveは直後に空白を
// 開けない慣行のため、空白を挟む通常のcommentは除去対象のまま扱う。
func roundDirectiveComment(rest []byte) bool {
	if len(rest) == 0 || !isDirectiveIdentChar(rest[0], true) {
		return false
	}
	for i := 1; i < len(rest); i++ {
		if rest[i] == ':' {
			return true
		}
		if !isDirectiveIdentChar(rest[i], false) {
			return false
		}
	}
	return false
}

func isDirectiveIdentChar(char byte, first bool) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
		return true
	case char >= '0' && char <= '9':
		return !first
	}
	return false
}

func roundDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// RoundDeltaは1 review roundの前境界に対する状態差分の機械分類結果。DocPathsは
// 文書pathの追加・変更・削除へ関与したpath数。
type RoundDelta struct {
	Class         string
	ChangedPaths  int
	SemanticPaths int
	DocPaths      int
}

// CompareRoundRecordsはcurr roundの前round(またはbaseline)recordに対する差分を
// 分類する。prevが無いときはinitial、分類情報が欠けたときはunknownへ倒す。
// 同一snapshot判定は3軸digestだけで確定し、path分類の失敗に影響されない。
func CompareRoundRecords(prev, curr *RoundRecord) RoundDelta {
	if curr == nil {
		return RoundDelta{Class: RoundDeltaUnknown}
	}
	if curr.WorkerPhase == RoundWorkerPhaseBaseline {
		return RoundDelta{Class: RoundDeltaBaseline}
	}
	if prev == nil {
		return RoundDelta{Class: RoundDeltaInitial}
	}
	if roundSnapshotsEqual(prev.Snapshot, curr.Snapshot) {
		return RoundDelta{Class: RoundDeltaSameSnapshot}
	}
	if prev.CaptureError != "" || curr.CaptureError != "" {
		return RoundDelta{Class: RoundDeltaUnknown}
	}
	previous := roundPathIndex(prev.Paths)
	current := roundPathIndex(curr.Paths)
	delta := RoundDelta{Class: RoundDeltaCommentFormat}
	for path, currEntry := range current {
		prevEntry, existed := previous[path]
		if existed && prevEntry.FullDigest == currEntry.FullDigest && currEntry.FullDigest != "" {
			continue
		}
		delta.ChangedPaths++
		if currEntry.Class == RoundPathClassDoc || (existed && prevEntry.Class == RoundPathClassDoc) {
			delta.addDocPath()
			continue
		}
		if roundPathDeltaNonSemantic(prevEntry, existed, currEntry) {
			continue
		}
		delta.SemanticPaths++
		delta.Class = RoundDeltaSemantic
	}
	for path := range previous {
		if _, ok := current[path]; ok {
			continue
		}
		delta.ChangedPaths++
		if previous[path].Class == RoundPathClassDoc {
			delta.addDocPath()
			continue
		}
		delta.SemanticPaths++
		delta.Class = RoundDeltaSemantic
	}
	if delta.ChangedPaths == 0 {
		return RoundDelta{Class: RoundDeltaUnknown}
	}
	return delta
}

// addDocPathは文書pathの差分1件をdoc-change側へ計上する。文書はAGENTS・instructions・
// EVAL等の行動規定を含み得るため内容差の意味を機械確定せず、意味差分が既に観測されて
// いるときだけsemantic-changeを優先する。
func (d *RoundDelta) addDocPath() {
	d.DocPaths++
	if d.Class != RoundDeltaSemantic {
		d.Class = RoundDeltaDocChange
	}
}

// roundPathDeltaNonSemanticはcode path 1件の差分がcomment/formatだけと機械確定
// できるかを返す。正規化digestが両側観測済みで一致するときだけtrueとし、追加・削除・
// 未観測・言語非対応はfalse(semantic候補)へ倒す。文書pathはdoc-change側へ先に
// 振り分けられるためここへ来ない。
func roundPathDeltaNonSemantic(prev RoundPathState, existed bool, curr RoundPathState) bool {
	if !existed || curr.Deleted || prev.Deleted {
		return false
	}
	return prev.SemanticDigest != "" && curr.SemanticDigest != "" &&
		prev.SemanticDigest == curr.SemanticDigest
}

func roundPathIndex(paths []RoundPathState) map[string]RoundPathState {
	index := make(map[string]RoundPathState, len(paths))
	for _, entry := range paths {
		index[entry.Path] = entry
	}
	return index
}

// roundSnapshotsEqualは両recordの3軸digestが全て観測済みで一致するときだけtrue。
// 空digestを一致扱いにすると未観測を同一状態と誤るため、index/worktreeは非空を要求する。
func roundSnapshotsEqual(prev, curr SnapshotDigest) bool {
	return prev.IndexDigest != "" && prev.WorktreeDigest != "" &&
		curr.IndexDigest != "" && curr.WorktreeDigest != "" &&
		prev.Head == curr.Head &&
		prev.IndexDigest == curr.IndexDigest &&
		prev.WorktreeDigest == curr.WorktreeDigest
}
