// Package reposearchはworking tree現物を対象としたBM25によるrepo内検索coreを提供
// する。CLI・prompt・telemetry等の呼び出し規約からは独立し、検索毎にfingerprintで
// freshnessを検証し、cacheはschema・policy version・repo・fingerprint完全一致の時だけ
// 再利用する。
package reposearch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultMaxResults    = 20
	hardMaxResults       = 100
	defaultMaxFiles      = 50_000
	hardMaxFiles         = 50_000
	defaultMaxTotalBytes = 256 << 20
	hardMaxTotalBytes    = 256 << 20
	defaultPathWeight    = 0.5
	maxSearchAttempts    = 2
)

type CacheStatus string

const (
	CacheStatusHit          CacheStatus = "hit"
	CacheStatusRebuilt      CacheStatus = "rebuilt"
	CacheStatusWriteWarning CacheStatus = "write-warning"
)

// Resultは1件の検索結果。ScoreはContentScore+PathScore、PathScoreはPathWeight適用後の
// path寄与で、同点はPath昇順。Lineはsnippetの1-based行番号で、pathのみ一致で内容行が
// 無い場合は0。
type Result struct {
	Path         string
	Score        float64
	ContentScore float64
	PathScore    float64
	Line         int
	Snippet      string
}

type Report struct {
	Results      []Result
	CacheStatus  CacheStatus
	Warnings     []string
	IndexedFiles int
	SkippedFiles int
}

// OptionsはSearchの動作設定。各上限は0で既定値、負数やhard cap超はErrInvalidOptions
// になる。
type Options struct {
	// CacheRootはcache保存directory。空なら既定(GLM_WORKER_HOME、未設定時は
	// ~/.glm-worker、配下のsearch)を使う。
	CacheRoot string
	// DisableCacheはcache読み書きを無効化する。CacheRootとの同時指定はerror。
	DisableCache bool
	// MaxResultsは0なら既定20。1..hardMaxResults(100)外はerrorで切り詰めない。
	MaxResults int
	// MaxFilesは列挙後の対象file数上限。0なら既定50,000。hard capと同値で、
	// 超過はErrIndexLimitとなり部分結果・cacheを返さない。
	MaxFiles int
	// MaxTotalBytesは読み込む対象内容の合計byte上限。0なら既定256MiB。超過は
	// ErrIndexLimitとなる。
	MaxTotalBytes int
	// PathWeightはpath一致BM25への重み。nilなら既定0.5。0以上の有限値だけ許可する。
	PathWeight *float64
	// ExcludeDirsは既定除外directoryへ追加するdirectory名。既定は削除できず、
	// 空・`.`・`..`・`/`含有・絶対pathはerror。
	ExcludeDirs []string
}

var (
	ErrEmptyQuery     = errors.New("queryをtoken化しても空です")
	ErrIndexRace      = errors.New("検索中にrepository状態が変化しました")
	ErrInvalidOptions = errors.New("reposearchのOptionsが不正です")
	ErrIndexLimit     = errors.New("検索対象がOptions上限を超えています")
)

type searchSettings struct {
	cacheRoot     string
	cacheDisabled bool
	limit         int
	maxFiles      int
	maxTotalBytes int
	pathWeight    float64
	excludeDirs   map[string]bool
}

// SearchはrepoRoot(subdir指定ならそのgit toplevel)のworking tree現物をqueryで検索する。
// committed HEADだけでなくmodified・staged・untrackedの現物を反映し、deleted・rename旧
// pathは検索しない。fingerprintが検索中に変化した場合は1回だけ全体をやり直し、
// 再変化すればErrIndexRaceとして混合結果を返さない。ctx取消はgit subprocessを中断する。
func Search(ctx context.Context, repoRoot string, query string, opts Options) (Report, error) {
	settings, err := resolveSettings(opts)
	if err != nil {
		return Report{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return Report{}, ErrEmptyQuery
	}
	root, err := resolveCanonicalRoot(ctx, repoRoot)
	if err != nil {
		return Report{}, err
	}
	for attempt := 0; attempt < maxSearchAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		report, raced, err := attemptSearch(ctx, root, queryTokens, settings)
		if err != nil {
			return Report{}, err
		}
		if !raced {
			return report, nil
		}
	}
	return Report{}, ErrIndexRace
}

func resolveSettings(opts Options) (searchSettings, error) {
	limit, err := resolveBound(opts.MaxResults, defaultMaxResults, hardMaxResults, "MaxResults")
	if err != nil {
		return searchSettings{}, err
	}
	maxFiles, err := resolveBound(opts.MaxFiles, defaultMaxFiles, hardMaxFiles, "MaxFiles")
	if err != nil {
		return searchSettings{}, err
	}
	maxTotalBytes, err := resolveBound(opts.MaxTotalBytes, defaultMaxTotalBytes, hardMaxTotalBytes, "MaxTotalBytes")
	if err != nil {
		return searchSettings{}, err
	}
	pathWeight := defaultPathWeight
	if opts.PathWeight != nil {
		pathWeight = *opts.PathWeight
		if pathWeight < 0 || math.IsNaN(pathWeight) || math.IsInf(pathWeight, 0) {
			return searchSettings{}, fmt.Errorf("%w: PathWeightは0以上の有限値を指定してください: %v", ErrInvalidOptions, *opts.PathWeight)
		}
	}
	excludeDirs, err := resolveExcludeDirs(opts.ExcludeDirs)
	if err != nil {
		return searchSettings{}, err
	}
	if opts.DisableCache && opts.CacheRoot != "" {
		return searchSettings{}, fmt.Errorf("%w: DisableCacheとCacheRootは同時指定できません", ErrInvalidOptions)
	}
	cacheRoot := opts.CacheRoot
	if cacheRoot == "" && !opts.DisableCache {
		if cacheRoot, err = defaultCacheRoot(); err != nil {
			return searchSettings{}, err
		}
	}
	return searchSettings{
		cacheRoot:     cacheRoot,
		cacheDisabled: opts.DisableCache,
		limit:         limit,
		maxFiles:      maxFiles,
		maxTotalBytes: maxTotalBytes,
		pathWeight:    pathWeight,
		excludeDirs:   excludeDirs,
	}, nil
}

func resolveBound(requested, defaultValue, hardCap int, name string) (int, error) {
	switch {
	case requested == 0:
		return defaultValue, nil
	case requested < 0 || requested > hardCap:
		return 0, fmt.Errorf("%w: %sは0..%dで指定してください: %d", ErrInvalidOptions, name, hardCap, requested)
	default:
		return requested, nil
	}
}

// defaultCacheRootはconfig.LoadのstateHome規則(GLM_WORKER_HOME、未設定時~/.glm-worker)
// と同じ位置へsessionsと並ぶsearch directoryを返す。core単独でも既定cacheが
// 実homeへ散らばらないようenv規則だけを共有する。
func defaultCacheRoot() (string, error) {
	if home := os.Getenv("GLM_WORKER_HOME"); home != "" {
		return filepath.Join(home, "search"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("既定cache rootを解決できません: %w", err)
	}
	return filepath.Join(home, ".glm-worker", "search"), nil
}

// attemptSearchはfingerprint一致の下で1回の検索を実行する。cache読込・(miss時)
// rebuild後とranking/snippet生成後の2点でfingerprintを再確認し、不一致なら結果も
// cache書込みも行わない。最終確認後のcache atomic書込み失敗時だけは結果を返し、
// CacheStatusWriteWarningとwarningで明示する。
func attemptSearch(ctx context.Context, root string, queryTokens []string, settings searchSettings) (Report, bool, error) {
	before, err := computeFingerprint(ctx, root, settings.excludeDirs)
	if err != nil {
		return Report{}, false, err
	}
	index, hit := loadIndex(settings, root, before)
	if !hit {
		index, err = rebuildIndex(ctx, root, settings)
		if err != nil {
			return Report{}, false, err
		}
	}
	raced, err := fingerprintUnchanged(ctx, root, settings.excludeDirs, before)
	if err != nil || raced {
		return Report{}, raced, err
	}
	results := rankDocuments(index.docs, queryTokens, settings.limit, settings.pathWeight)
	warnings := attachSnippets(root, results, queryTokens)
	raced, err = fingerprintUnchanged(ctx, root, settings.excludeDirs, before)
	if err != nil || raced {
		return Report{}, raced, err
	}
	status := CacheStatusRebuilt
	if hit {
		status = CacheStatusHit
	} else if !settings.cacheDisabled {
		if writeErr := writeIndex(settings, root, before, index); writeErr != nil {
			status = CacheStatusWriteWarning
			warnings = append(warnings, fmt.Sprintf("cacheを書き込めません: %v", writeErr))
		}
	}
	return Report{
		Results:      results,
		CacheStatus:  status,
		Warnings:     warnings,
		IndexedFiles: index.indexed,
		SkippedFiles: index.skipped,
	}, false, nil
}

// resolveCanonicalRootはrepoRootを絶対path・symlink評価したgit toplevelとして返す。
// cacheのrepo同一性判定はこのcanonical pathで行う。
func resolveCanonicalRoot(ctx context.Context, repoRoot string) (string, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("repo rootを絶対pathへ解決できません: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("repo rootを解決できません: %w", err)
	}
	output, err := gitOutput(ctx, canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git repositoryではありません: %s: %w", canonical, err)
	}
	root, err := filepath.EvalSymlinks(strings.TrimSpace(string(output)))
	if err != nil {
		return "", fmt.Errorf("git toplevelを解決できません: %w", err)
	}
	return filepath.Clean(root), nil
}
