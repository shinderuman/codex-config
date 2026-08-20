package reposearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mutateDuringCaptureはcaptureFingerprintの指定呼び出しでだけrepoのuntracked fileを
// 書き替えてから本物のfingerprintを取り、検索途中のworking tree変化を再現する。
// 呼び出し毎に内容を変えないと2度目の変化が同一状態になりfingerprintに現れない。
func mutateDuringCapture(t *testing.T, dir string, mutateAt func(call int) bool) {
	t.Helper()
	original := captureFingerprint
	call := 0
	captureFingerprint = func(ctx context.Context, repoRoot string) (fingerprint, error) {
		call++
		if mutateAt(call) {
			writeTestFile(t, filepath.Join(dir, "raced.txt"), fmt.Sprintf("needle raced %d\n", call))
		}
		return original(ctx, repoRoot)
	}
	t.Cleanup(func() { captureFingerprint = original })
}

func TestSearchRetriesOnceOnMidSearchMutation(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	// 1試行目のrebuild後確認(2回目呼び出し)でだけ変化させ、2試行目は成功させる。
	mutateDuringCapture(t, dir, func(call int) bool { return call == 2 })

	report, err := Search(context.Background(), dir, "needle", Options{CacheRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultPaths(report); !reflect.DeepEqual(got, []string{"a.txt", "raced.txt"}) {
		t.Fatalf("results = %v want 変化後の状態を反映した [a.txt raced.txt]", got)
	}
}

func TestSearchFailsClosedOnRepeatedMutation(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	// 各試行のrebuild後確認(2・5回目呼び出し)で変化させ続け、両試行をraceさせる。
	mutateDuringCapture(t, dir, func(call int) bool { return call == 2 || call == 5 })

	if _, err := Search(context.Background(), dir, "needle", Options{CacheRoot: t.TempDir()}); !errors.Is(err, ErrIndexRace) {
		t.Fatalf("error = %v want ErrIndexRace", err)
	}
}

func TestSearchRaceDoesNotWriteMixedCache(t *testing.T) {
	dir := initRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle one\n")
	commitAll(t, dir, "init")
	cacheRoot := t.TempDir()
	mutateDuringCapture(t, dir, func(call int) bool { return call == 2 || call == 5 })

	if _, err := Search(context.Background(), dir, "needle", Options{CacheRoot: cacheRoot}); !errors.Is(err, ErrIndexRace) {
		t.Fatalf("error = %v want ErrIndexRace", err)
	}
	if _, err := os.Stat(canonicalCachePath(t, cacheRoot, dir)); !os.IsNotExist(err) {
		t.Fatal("race失敗時にcacheが書かれています")
	}
}
