package reposearch

import (
	"strings"
	"unicode"
)

// tokenizerVersionはtoken化規約の変更をcache無効化へ反映するための版。
const tokenizerVersion = 1

// tokenizeはtextを検索token列へ変換する。英数字(unicode letter/digit)の連続は
// lowercaseした1 tokenとし、CJK文字の連続はbigram(1文字ならその文字)へ展開する。
// query・file内容・pathで同じ変換を用いるため、file名区切りの`/`・`_`・`-`・`.`も
// ここで自然にtoken境界になる。不正UTF-8 byteは境界扱いとし読み飛ばす。
func tokenize(text string) []string {
	var tokens []string
	var run []rune
	flush := func() {
		if len(run) == 0 {
			return
		}
		if isCJK(run[0]) {
			tokens = append(tokens, cjkGrams(run)...)
		} else {
			tokens = append(tokens, strings.ToLower(string(run)))
		}
		run = run[:0]
	}
	for _, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(run) > 0 && isCJK(r) != isCJK(run[len(run)-1]) {
			flush()
		}
		run = append(run, r)
	}
	flush()
	return tokens
}

// katakanaProlongedSoundMarkは「ー」(U+30FC)。script上はCommon扱いのため
// unicode.Katakanaに含まれず、片仮語の連続を切らないためにここへ加える。
var katakanaProlongedSoundMark = &unicode.RangeTable{
	R16: []unicode.Range16{{Lo: 0x30FC, Hi: 0x30FC, Stride: 1}},
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		unicode.Is(katakanaProlongedSoundMark, r)
}

// cjkGramsはCJK連続runをbigramへ展開する。前後の文脈を持たない1文字runは
// そのまま1 tokenとする。
func cjkGrams(run []rune) []string {
	if len(run) == 1 {
		return []string{string(run)}
	}
	grams := make([]string, 0, len(run)-1)
	for i := 0; i+1 < len(run); i++ {
		grams = append(grams, string(run[i:i+2]))
	}
	return grams
}

func termFrequencies(tokens []string) map[string]int {
	if len(tokens) == 0 {
		return nil
	}
	counts := make(map[string]int, len(tokens))
	for _, token := range tokens {
		counts[token]++
	}
	return counts
}
