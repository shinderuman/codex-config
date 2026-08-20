package reposearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

const (
	cacheSchemaVersion = 2
	cacheFileName      = "index.json"
)

// cacheDataはcache file 1つへ保存する全情報。schema・policy version・canonical
// repo_root・fingerprintのすべてが現repositoryと一致する時だけcacheを使うため、
// 別repo-root marker fileは持たない。docsはtoken統計だけで、file本文・snippet等の
// raw sourceは保存しない。
type cacheData struct {
	SchemaVersion      int      `json:"schema_version"`
	RepoRoot           string   `json:"repo_root"`
	TokenizerVersion   int      `json:"tokenizer_version"`
	EnumerationVersion int      `json:"enumeration_version"`
	ExcludeDirs        []string `json:"exclude_dirs"`
	Head               string   `json:"head"`
	IndexDigest        string   `json:"index_digest"`
	WorktreeDigest     string   `json:"worktree_digest"`
	IndexedFiles       int      `json:"indexed_files"`
	IndexedBytes       int      `json:"indexed_bytes"`
	SkippedFiles       int      `json:"skipped_files"`
	Docs               []doc    `json:"docs"`
}

// sortedExcludeDirsは実効除外集合(既定+Options.ExcludeDirs)を決定的な並びへ整列する。
// cache identityの一部として保存・比較するため、mapの走査順に依らない固定形にする。
func sortedExcludeDirs(dirs map[string]bool) []string {
	names := make([]string, 0, len(dirs))
	for name := range dirs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// cachePathForはcacheRoot配下へrepo canonical pathのsha256でdirを切る。state packageの
// RepoHash規則と同じで、repo移動後は別dirになり旧cacheが混入しない。
func cachePathFor(cacheRoot, repoRoot string) string {
	hash := sha256.Sum256([]byte(repoRoot))
	return filepath.Join(cacheRoot, hex.EncodeToString(hash[:]), cacheFileName)
}

// loadIndexはsettingsのcacheを読み、現repositoryとの完全一致時だけその中身を返す。
// 欠損・corruption・parse失敗・schema/version不一致・repo_root不一致・fingerprint
// 不一致・doc統計の意味的異常は区別せずmiss(rebuild)扱いにし、stale結果は使わない。
// 上限より大きいcacheもmissにし、上限を下げた呼出側で一貫してErrIndexLimitへ向かう。
func loadIndex(settings searchSettings, repoRoot string, fp fingerprint) (builtIndex, bool) {
	if settings.cacheDisabled {
		return builtIndex{}, false
	}
	data, err := os.ReadFile(cachePathFor(settings.cacheRoot, repoRoot))
	if err != nil {
		return builtIndex{}, false
	}
	var cached cacheData
	if err := json.Unmarshal(data, &cached); err != nil {
		return builtIndex{}, false
	}
	if !cacheMatchesRepository(cached, repoRoot, fp, settings) {
		return builtIndex{}, false
	}
	return builtIndex{
		docs:         cached.Docs,
		indexed:      cached.IndexedFiles,
		indexedBytes: cached.IndexedBytes,
		skipped:      cached.SkippedFiles,
	}, true
}

func cacheMatchesRepository(cached cacheData, repoRoot string, fp fingerprint, settings searchSettings) bool {
	if cached.SchemaVersion != cacheSchemaVersion || cached.RepoRoot != repoRoot {
		return false
	}
	if cached.TokenizerVersion != tokenizerVersion || cached.EnumerationVersion != enumerationVersion {
		return false
	}
	// 除外policyが異なるcacheは収集対象が異なるため、たとえfingerprintが一致して
	// いても再利用しない。実効集合(既定は除去不可)との完全一致を要求する。
	if !slices.Equal(cached.ExcludeDirs, sortedExcludeDirs(settings.excludeDirs)) {
		return false
	}
	if cached.Head != fp.Head || cached.IndexDigest != fp.IndexDigest || cached.WorktreeDigest != fp.WorktreeDigest {
		return false
	}
	if cached.IndexedFiles != len(cached.Docs) || cached.IndexedFiles < 0 || cached.SkippedFiles < 0 {
		return false
	}
	if cached.IndexedBytes < 0 || cached.IndexedFiles > settings.maxFiles || cached.IndexedBytes > settings.maxTotalBytes {
		return false
	}
	for i, entry := range cached.Docs {
		if entry.Path == "" || filepath.ToSlash(entry.Path) != entry.Path {
			return false
		}
		if entry.ContentLength < 0 || entry.PathLength < 0 {
			return false
		}
		if excludedPath(entry.Path, settings.excludeDirs) {
			return false
		}
		if _, err := joinWithinRoot(repoRoot, entry.Path); err != nil {
			return false
		}
		if i > 0 && cached.Docs[i-1].Path >= entry.Path {
			return false
		}
		if !validTermFrequencies(entry.ContentTF, entry.ContentLength) || !validTermFrequencies(entry.PathTF, entry.PathLength) {
			return false
		}
	}
	return true
}

// validTermFrequenciesはTF統計の意味的整合を見る。countは正整数のみ、その合計が
// lengthと一致し、length 0なら空である。 tampered cacheを検出してmissへ落とす。
func validTermFrequencies(frequencies map[string]int, length int) bool {
	var sum int64
	for _, count := range frequencies {
		if count <= 0 {
			return false
		}
		sum += int64(count)
		if sum > int64(length) {
			return false
		}
	}
	return sum == int64(length)
}

// writeIndexはrebuild結果を単一JSONへ原子的に書き込む。呼出側でfingerprint一致を
// 確認した後だけ呼ばれ、混合状態のcacheは書かない。
func writeIndex(settings searchSettings, repoRoot string, fp fingerprint, index builtIndex) error {
	if settings.cacheRoot == "" {
		return nil
	}
	docs := make([]doc, len(index.docs))
	copy(docs, index.docs)
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	data, err := json.Marshal(cacheData{
		SchemaVersion:      cacheSchemaVersion,
		RepoRoot:           repoRoot,
		TokenizerVersion:   tokenizerVersion,
		EnumerationVersion: enumerationVersion,
		ExcludeDirs:        sortedExcludeDirs(settings.excludeDirs),
		Head:               fp.Head,
		IndexDigest:        fp.IndexDigest,
		WorktreeDigest:     fp.WorktreeDigest,
		IndexedFiles:       len(docs),
		IndexedBytes:       index.indexedBytes,
		SkippedFiles:       index.skipped,
		Docs:               docs,
	})
	if err != nil {
		return fmt.Errorf("cacheをJSON化できません: %w", err)
	}
	return writeFileAtomic(cachePathFor(settings.cacheRoot, repoRoot), append(data, '\n'), 0o600)
}

// writeFileAtomicは同directoryのtemp fileへ書き込み後renameする。state packageの
// 同名helperと同じ規則で、部分書込み状態を利用者へ見せない。
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()

	defer func() {
		file.Close()
		os.Remove(tempPath)
	}()

	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
