package reposearch

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "ascii lowercasing and separators", input: "Foo-Bar_baz.qux/quux", want: []string{"foo", "bar", "baz", "qux", "quux"}},
		{name: "digits stay in run", input: "Go1.22 ray 42", want: []string{"go1", "22", "ray", "42"}},
		{name: "cjk run becomes bigrams", input: "日本語の検索", want: []string{"日本", "本語", "語の", "の検", "検索"}},
		{name: "single cjk rune", input: "漢", want: []string{"漢"}},
		{name: "mixed runs split at cjk boundary", input: "abc漢字def", want: []string{"abc", "漢字", "def"}},
		{name: "katakana and hangul runs", input: "ビール 맥주", want: []string{"ビー", "ール", "맥주"}},
		{name: "invalid utf-8 bytes are boundaries", input: "\xff\xfealpha", want: []string{"alpha"}},
		{name: "empty and separator only", input: "。。。  \t\n", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tokenize(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tokenize(%q) = %v want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestTokenizeLowercasesForCaseInsensitiveMatch(t *testing.T) {
	if got, want := tokenize("SNAPSHOT.go"), tokenize("snapshot.GO"); !reflect.DeepEqual(got, want) {
		t.Fatalf("大文字小違いが同一tokenになりません: %v vs %v", got, want)
	}
}
