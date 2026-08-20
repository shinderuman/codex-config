package reposearch

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	// Okapi BM25の標準的parameters。
	bm25K1 = 1.2
	bm25B  = 0.75

	maxSnippetRunes = 200
)

type fieldStatistics struct {
	averageLength float64
	frequencies   map[string]int
}

// rankDocumentsはdocsをBM25で順位付けする。content・path各fieldを独立した統計
// (文書長・df)で評価し、PathScore=pathWeight×path BM25、Score=ContentScore+PathScore
// とする。同点はpath昇順で固定し、doc列はpath順に並んでいる前提で先頭から走査して
// 演算順も固定する。
func rankDocuments(docs []doc, queryTokens []string, limit int, pathWeight float64) []Result {
	if len(docs) == 0 {
		return nil
	}
	contentStats := buildFieldStatistics(docs, queryTokens, func(entry doc) map[string]int { return entry.ContentTF }, func(entry doc) int { return entry.ContentLength })
	pathStats := buildFieldStatistics(docs, queryTokens, func(entry doc) map[string]int { return entry.PathTF }, func(entry doc) int { return entry.PathLength })
	var results []Result
	for _, entry := range docs {
		contentScore := bm25FieldScore(entry.ContentTF, entry.ContentLength, contentStats, len(docs), queryTokens)
		pathScore := pathWeight * bm25FieldScore(entry.PathTF, entry.PathLength, pathStats, len(docs), queryTokens)
		if contentScore+pathScore <= 0 {
			continue
		}
		results = append(results, Result{
			Path:         entry.Path,
			Score:        contentScore + pathScore,
			ContentScore: contentScore,
			PathScore:    pathScore,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Path < results[j].Path
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func buildFieldStatistics(docs []doc, tokens []string, frequencies func(doc) map[string]int, length func(doc) int) fieldStatistics {
	total := 0
	for _, entry := range docs {
		total += length(entry)
	}
	seen := map[string]bool{}
	documentFrequency := make(map[string]int, len(tokens))
	for _, token := range tokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		count := 0
		for _, entry := range docs {
			if frequencies(entry)[token] > 0 {
				count++
			}
		}
		documentFrequency[token] = count
	}
	return fieldStatistics{averageLength: float64(total) / float64(len(docs)), frequencies: documentFrequency}
}

// bm25FieldScoreは1 field分のBM25をquery token順に加算する。idfは常に非負になる
// 形式のため、 corpusに多数出現するtokenが負へscoreを反転させることはない。
// termFreq>0のdocは必ずfield長も持つためaverageLength>0が保証される。
func bm25FieldScore(termFrequencies map[string]int, docLength int, stats fieldStatistics, docCount int, tokens []string) float64 {
	score := 0.0
	for _, token := range tokens {
		termFreq := termFrequencies[token]
		if termFreq == 0 {
			continue
		}
		idf := math.Log(1 + (float64(docCount-stats.frequencies[token])+0.5)/(float64(stats.frequencies[token])+0.5))
		lengthNorm := 1 - bm25B + bm25B*float64(docLength)/stats.averageLength
		score += idf * (float64(termFreq) * (bm25K1 + 1)) / (float64(termFreq) + bm25K1*lengthNorm)
	}
	return score
}

// attachSnippetsは上位結果のfileからquery tokenが最も集中する行をsnippetとして取り出す。
// pathのみの一致や対象外fileはLine=0・Snippet=""のままにし、読み取り失敗だけwarningへ残す。
func attachSnippets(root string, results []Result, queryTokens []string) []string {
	var warnings []string
	for i := range results {
		abs, err := joinWithinRoot(root, results[i].Path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("snippet対象pathがrepository境界を越えています: %s: %v", results[i].Path, err))
			continue
		}
		content, ok, err := readSearchableFile(abs)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("snippet生成に失敗しました: %s: %v", results[i].Path, err))
			continue
		}
		if !ok {
			continue
		}
		results[i].Line, results[i].Snippet = bestLine(string(content), queryTokens)
	}
	return warnings
}

// bestLineはquery token出現数が最大の行を1-based行番号付きで返す。同点は先頭行。
// 内容にquery tokenが無い(pathのみ一致)場合は行番号0と空snippetを返す。
func bestLine(content string, queryTokens []string) (int, string) {
	bestNumber, bestCount, bestText := 0, 0, ""
	for i, line := range strings.Split(content, "\n") {
		count := lineTokenScore(line, queryTokens)
		if count > bestCount {
			bestNumber, bestText, bestCount = i+1, line, count
		}
	}
	if bestCount == 0 {
		return 0, ""
	}
	return bestNumber, truncateSnippet(strings.TrimSpace(strings.TrimSuffix(bestText, "\r")))
}

func lineTokenScore(line string, queryTokens []string) int {
	counts := termFrequencies(tokenize(line))
	total := 0
	for _, token := range queryTokens {
		total += counts[token]
	}
	return total
}

func truncateSnippet(line string) string {
	runes := []rune(line)
	if len(runes) <= maxSnippetRunes {
		return line
	}
	return string(runes[:maxSnippetRunes-3]) + "..."
}
