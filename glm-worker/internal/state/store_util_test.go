package state

import (
	"path/filepath"
	"testing"
)

// TestValidGeneratedUUIDAcceptsNewUUIDOutputは生成function自身の出力形式を常に受け付ける。
func TestValidGeneratedUUIDAcceptsNewUUIDOutput(t *testing.T) {
	for i := 0; i < 32; i++ {
		id, err := NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidGeneratedUUID(id) {
			t.Fatalf("NewUUID出力 %qが生成形式として拒否されました", id)
		}
		if filepath.Base(id) != id {
			t.Fatalf("NewUUID出力 %qがpath要素として安全な形式ではありません", id)
		}
	}
}

// TestValidGeneratedUUIDRejectsForeignFormsは生成形式の外にある値をすべて拒否する。
// 拒否だけが境界であり、ここではfilesystemへ触らない。
func TestValidGeneratedUUIDRejectsForeignForms(t *testing.T) {
	for _, id := range []string{
		"",
		"none",
		"..",
		"../..",
		"../../evil",
		"../../../state/events/other",
		"/tmp/evil",
		"evil.jsonl",
		".hidden",
		"12345678-1234-4234-8123-123456789abc-extra",
		"12345678123442348123123456789abc",
		"12345678_1234_4234_8123_123456789abc",
		"1234567g-1234-4234-8123-123456789abc",
		"12345678-1234-4234-8123-123456789abC",
		"12345678-1234-1234-8123-123456789abc",
		"12345678-1234-4234-c123-123456789abc",
		"12345678-1234-4234-4123-123456789abc",
		"１２３４５６７８-１２３４-４２３４-８１２３-１２３４５６７８９ａｂｃ",
	} {
		if ValidGeneratedUUID(id) {
			t.Fatalf("生成形式外の値 %qが受け入れられました", id)
		}
	}
	if !ValidGeneratedUUID("12345678-1234-4234-8123-123456789abc") {
		t.Fatal("canonical UUID v4形式が拒否されました")
	}
	if !ValidGeneratedUUID("12345678-1234-4234-a123-123456789abc") {
		t.Fatal("variant 10xのcanonical UUID v4形式が拒否されました")
	}
}
