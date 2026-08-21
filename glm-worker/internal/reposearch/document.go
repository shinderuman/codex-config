package reposearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
)

const (
	maxFileBytes     = 1 << 20
	binarySniffBytes = 8 << 10
)

// docは1 file分の検索統計。cacheへこのまま永続化するため生内容・snippetは持たせない。
type doc struct {
	Path          string         `json:"path"`
	ContentLength int            `json:"content_length"`
	PathLength    int            `json:"path_length"`
	ContentTF     map[string]int `json:"content_tf"`
	PathTF        map[string]int `json:"path_tf"`
}

type builtIndex struct {
	docs         []doc
	indexed      int
	indexedBytes int
	skipped      int
}

func rebuildIndex(ctx context.Context, repoRoot string, settings searchSettings) (builtIndex, error) {
	paths, err := enumerateFiles(ctx, repoRoot, settings.excludeDirs)
	if err != nil {
		return builtIndex{}, err
	}
	if len(paths) > settings.maxFiles {
		return builtIndex{}, fmt.Errorf("%w: 対象file数 %d がMaxFiles %dを超えています", ErrIndexLimit, len(paths), settings.maxFiles)
	}
	index := builtIndex{docs: make([]doc, 0, len(paths))}
	totalBytes := 0
	for _, rel := range paths {
		abs, err := joinWithinRoot(repoRoot, rel)
		if err != nil {
			return builtIndex{}, err
		}
		content, outcome, err := readSearchableFile(abs)
		if err != nil {
			return builtIndex{}, err
		}
		if outcome != readIndexed {
			index.skipped++
			continue
		}
		totalBytes += len(content)
		if totalBytes > settings.maxTotalBytes {
			return builtIndex{}, fmt.Errorf("%w: 読み込み合計 %d bytes がMaxTotalBytes %dを超えています", ErrIndexLimit, totalBytes, settings.maxTotalBytes)
		}
		contentTokens := tokenize(string(content))
		pathTokens := tokenize(rel)
		index.docs = append(index.docs, doc{
			Path:          rel,
			ContentLength: len(contentTokens),
			PathLength:    len(pathTokens),
			ContentTF:     termFrequencies(contentTokens),
			PathTF:        termFrequencies(pathTokens),
		})
	}
	index.indexed = len(index.docs)
	index.indexedBytes = totalBytes
	return index, nil
}

// readOutcomeはfile1つが検索/index対象corpusへどう現れるか。rebuildのskip計上と
// fingerprintのfreshness評価が同じ分類を共有するための区分。
type readOutcome int

const (
	readIndexed readOutcome = iota
	readSkipped
	readMissing
)

// readSearchableFileは検索対象として読める通常fileの内容を返す。symlink・FIFO等の
// 特殊file、maxFileBytes超、先頭binarySniffBytes内にNULを含むfileはreadSkippedとして
// 内容を読まず対象外扱いにする。tracked pathのworking treeからの消失(deleted・rename
// 旧path)はreadMissingで区別する。Lstatで対象を確認してから読むため、特殊fileを
// 読み切ってhangさせることはない。読み込むのはmaxFileBytes以下の対象file本文だけで、
// fingerprintもこの関数経由で同じ上限内の読み込みに限る。
func readSearchableFile(abs string) ([]byte, readOutcome, error) {
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, readMissing, nil
	}
	if err != nil {
		return nil, readSkipped, fmt.Errorf("%sをstatできません: %w", abs, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, readSkipped, nil
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return nil, readSkipped, fmt.Errorf("%sを読めません: %w", abs, err)
	}
	if bytes.IndexByte(content[:min(len(content), binarySniffBytes)], 0) >= 0 {
		return nil, readSkipped, nil
	}
	return content, readIndexed, nil
}
