package abeval

import (
	"fmt"
	"strings"
	"testing"
)

func compareFixture() Comparison {
	spec := validSpec()
	return Compare(spec, validDirectRecord(spec), validOrchestratedRecord(spec))
}

func TestCodexReductionComputesFromActualUsage(t *testing.T) {
	reduction := compareFixture().CodexReduction
	if reduction.Status != codexReductionActual {
		t.Fatalf("status = %q want %q", reduction.Status, codexReductionActual)
	}
	if got, want := reduction.InputPercent, 60.0; got != want {
		t.Fatalf("input削減率 = %v want %v", got, want)
	}
	if got, want := reduction.OutputPercent, 53.333333333333336; got != want {
		t.Fatalf("output削減率 = %v want %v", got, want)
	}
}

func TestCodexReductionStaysUnknownWithoutActualUsage(t *testing.T) {
	tests := []struct {
		name         string
		direct       CodexUsage
		orchestrated CodexUsage
		wantReason   string
	}{
		{
			name:         "both unknown",
			direct:       CodexUsage{},
			orchestrated: CodexUsage{},
			wantReason:   "direct,orchestrated",
		},
		{
			name:         "orchestrated unknown",
			direct:       CodexUsage{Source: CodexUsageSourceAppExport, InputTokens: 100},
			orchestrated: CodexUsage{},
			wantReason:   "orchestrated",
		},
		{
			name:         "direct unknown",
			direct:       CodexUsage{},
			orchestrated: CodexUsage{Source: CodexUsageSourceAppExport, InputTokens: 100},
			wantReason:   "direct",
		},
		{
			name:         "actual but zero tokens",
			direct:       CodexUsage{Source: CodexUsageSourceAppExport},
			orchestrated: CodexUsage{Source: CodexUsageSourceAppExport},
			wantReason:   "零のため",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			reduction := codexReduction(test.direct, test.orchestrated)
			if reduction.Status != codexReductionUnknown {
				t.Fatalf("status = %q want unknown", reduction.Status)
			}
			if !strings.Contains(reduction.UnknownReason, test.wantReason) {
				t.Fatalf("reason = %q want substring %q", reduction.UnknownReason, test.wantReason)
			}
		})
	}
}

func TestCodexReductionReportsNegativeWhenOrchestratedUsesMore(t *testing.T) {
	reduction := codexReduction(
		CodexUsage{Source: CodexUsageSourceAppExport, InputTokens: 100},
		CodexUsage{Source: CodexUsageSourceAppExport, InputTokens: 150},
	)
	if reduction.InputPercent != -50.0 {
		t.Fatalf("input削減率 = %v want -50", reduction.InputPercent)
	}
}

func TestFormatKeepsCodexReductionQualityTimeAndGLMSeparate(t *testing.T) {
	out := Format(compareFixture())

	expectedKeys := []string{
		"SPEC: ",
		"MODES: ",
		"COMPARISON_METADATA: ",
		"MEASUREMENT_BOUNDARY: ",
		"ISOLATION: ",
		"CODEX_REDUCTION: ",
		"QUALITY_DELTA: ",
		"TIME: ",
		"CODEX_USAGE: ",
		"GLM_USAGE: ",
		"PROXY_METRICS: ",
		"NOTES: ",
	}
	actualKeys := make([]string, 0, len(expectedKeys))
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		key, _, _ := strings.Cut(line, ":")
		actualKeys = append(actualKeys, key+": ")
	}
	if fmt.Sprint(actualKeys) != fmt.Sprint(expectedKeys) {
		t.Fatalf("出力KEY構成 = %v want %v\n%s", actualKeys, expectedKeys, out)
	}
	if !strings.Contains(out, "CODEX_REDUCTION: input=60.0%, output=53.3% (actual usage") {
		t.Fatalf("CODEX_REDUCTION表示がactual usage基準ではありません:\n%s", out)
	}
	if !strings.Contains(out, "TIME: direct=1h32m0s; orchestrated=58m0s; delta=-34m0s") {
		t.Fatalf("TIME表示が計測境界導出と一致しません:\n%s", out)
	}
	if !strings.Contains(out, "GLM_USAGE: direct=not-used(direct modeはglm-worker委譲なし)") {
		t.Fatalf("direct modeのGLM未使用が表示されていません:\n%s", out)
	}
	if !strings.Contains(out, "合算値は算出しない") {
		t.Fatalf("GLM/Codex token非合算の方針が表示されていません:\n%s", out)
	}
}

func TestFormatKeepsProxyMetricsLabeledAndSeparateFromCodexUsage(t *testing.T) {
	out := Format(compareFixture())
	if !strings.Contains(out, "PROXY_METRICS: direct=none; orchestrated=sol-packet-bytes=812") {
		t.Fatalf("proxy指標がactual usageと区別された表示になっていません:\n%s", out)
	}
	if !strings.Contains(out, "CODEX_USAGE: direct=actual(source=codex-app-usage-export, input=1200000, output=90000); orchestrated=actual(source=codex-app-usage-export, input=480000, output=42000)") {
		t.Fatalf("CODEX_USAGEのactual/unknown区別が崩れています:\n%s", out)
	}
}

func TestFormatShowsUnknownReductionWithoutFabricatedPercent(t *testing.T) {
	spec := validSpec()
	direct := validDirectRecord(spec)
	direct.CodexUsage = CodexUsage{}
	orchestrated := validOrchestratedRecord(spec)
	out := Format(Compare(spec, direct, orchestrated))
	if !strings.Contains(out, "CODEX_REDUCTION: unknown (") {
		t.Fatalf("unknown削減率が推出力されていません:\n%s", out)
	}
	if strings.Contains(out, "input=%") {
		t.Fatalf("actual usageがないのに削減率percentが出力されています:\n%s", out)
	}
	if !strings.Contains(out, "CODEX_USAGE: direct=unknown") {
		t.Fatalf("direct usage unknownが表示されていません:\n%s", out)
	}
}
