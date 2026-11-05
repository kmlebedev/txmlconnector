package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
	"github.com/kmlebedev/txmlconnector/terminal/fingerprint"
	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
	"github.com/kmlebedev/txmlconnector/terminal/research"
	"github.com/kmlebedev/txmlconnector/terminal/volatility"
)

// testPattern is one complete draft used by the writer/promotion tests.
func testPattern() Pattern {
	from := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 6, 0)
	return Pattern{
		ID:          "rsi-reversion-sber",
		Title:       "RSI-возврат на SBER",
		Hypothesis:  "RSI(14) ниже 0.2 и цена выше SMA200 дают положительное ожидание на 5-минутных барах SBER.",
		Instruments: []string{"TQBR:SBER", "TQBR:GAZP"},
		Conditions: []string{
			"тренд: close > SMA(200)",
			"возврат: RSI(14) < 0.2",
		},
		Entry: "покупка на закрытии бара, где RSI(14) < 0.2 и close > SMA(200)",
		Exit:  "выход при RSI(14) > 0.8 или по стоп-лоссу 2%",
		Risk:  "стоп-лосс 2% от цены входа, не более 25% портфеля в позиции",
		Evidence: Evidence{
			DataFrom: from,
			DataTo:   to,
			AsOf:     to,
			Source:   SourceBars,
			OOS: backtest.Metrics{
				Trades: 42, From: from, To: to,
				TotalReturnPct: 4.5, MaxDrawdownPct: -2.1, Sharpe: 1.2, WinRatePct: 55,
			},
			DeflatedSharpe: 0.97,
			PBO:            0.12,
			Trials:         48,
			OOSTrades:      42,
			DataHash:       "sha256:deadbeef",
		},
		Status:    StatusDraft,
		CreatedAt: to.Add(24 * time.Hour),
	}
}

// parseDSHFrontmatter reads the YAML frontmatter the way the Harness skill
// loader does: the block between the --- fences, top-level keys and two nested
// metadata levels (metadata.status, metadata.evidence.<key>). The values are
// returned as raw strings (quotes are stripped by the caller via unquote).
func parseDSHFrontmatter(t *testing.T, raw string) map[string]string {
	t.Helper()
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		t.Fatalf("skill file must start with a --- frontmatter fence, got %q", firstLine(raw))
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatal("unterminated frontmatter: no closing --- fence")
	}
	values := map[string]string{}
	section := ""
	for _, line := range lines[1:end] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			t.Fatalf("frontmatter line without a key: %q", line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch indent {
		case 0:
			if value == "" {
				section = key
				continue
			}
			values[key] = value
			section = ""
		case 2:
			prefix := key
			if section != "" {
				prefix = section + "." + key
			}
			if value == "" {
				section = prefix
				continue
			}
			values[prefix] = value
		case 4:
			prefix := key
			if section != "" {
				prefix = section + "." + key
			}
			values[prefix] = value
		default:
			t.Fatalf("unexpected frontmatter indentation %d: %q", indent, line)
		}
	}
	return values
}

// firstLine is a small helper for the error messages.
func firstLine(raw string) string {
	if index := strings.IndexByte(raw, '\n'); index >= 0 {
		return raw[:index]
	}
	return raw
}

// unquote strips the YAML single/double quotes of a scalar.
func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// TestWriteDraftFrontmatterParsesInDSHFormat is the plan test: the draft is
// written as <dir>/<id>/SKILL.md with the Harness frontmatter (name and
// description are required) and the evidence metadata.
func TestWriteDraftFrontmatterParsesInDSHFormat(t *testing.T) {
	dir := t.TempDir()
	pattern := testPattern()
	path, err := WriteDraft(dir, pattern)
	if err != nil {
		t.Fatalf("WriteDraft: %v", err)
	}
	if want := filepath.Join(dir, pattern.ID, "SKILL.md"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	content := string(raw)
	values := parseDSHFrontmatter(t, content)
	for _, key := range []string{"name", "description", "whenToUse"} {
		if unquote(values[key]) == "" {
			t.Fatalf("frontmatter key %q is missing or empty: %v", key, values)
		}
	}
	if unquote(values["name"]) != pattern.ID {
		t.Fatalf("name = %q, want %q", unquote(values["name"]), pattern.ID)
	}
	if description := unquote(values["description"]); !strings.HasPrefix(description, "Use when") {
		t.Fatalf("description = %q, want a Harness matching hint starting with 'Use when'", description)
	}
	if got := unquote(values["metadata.status"]); got != StatusDraft {
		t.Fatalf("metadata.status = %q, want %q", got, StatusDraft)
	}
	for _, key := range []string{"dataFrom", "dataTo", "asOf", "source", "trials", "oosTrades", "deflatedSharpe", "pbo", "dataHash"} {
		if unquote(values["metadata.evidence."+key]) == "" {
			t.Fatalf("metadata.evidence.%s is missing", key)
		}
	}
	if got := unquote(values["metadata.evidence.source"]); got != SourceBars {
		t.Fatalf("evidence.source = %q", got)
	}
	// The body documents the paper-only limits of the skill (case-insensitive:
	// the bullets are capitalized Russian sentences).
	lower := strings.ToLower(content)
	for _, want := range []string{"# ", "## ограничения", "бумажн", "без гаранти", "не включает live", "только через pr"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("draft body is missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "LIVE") || strings.Contains(content, "реальн") && !strings.Contains(content, "не включает") {
		t.Fatalf("draft must not promise live trading:\n%s", content)
	}
}

// TestSkillPromotionRequiresOOS is the Review Focus 4 test: a draft without an
// out-of-sample window or with too few OOS trades never becomes validated.
func TestSkillPromotionRequiresOOS(t *testing.T) {
	pattern := testPattern()
	// No OOS window and no OOS trades at all.
	pattern.Evidence = Evidence{Source: SourceBars, DeflatedSharpe: 0.99, PBO: 0.1, Trials: 10, DataHash: "sha256:feed"}
	if err := Promote(&pattern, PromotionPolicy{}); err == nil {
		t.Fatal("a pattern without an out-of-sample window must not be promoted")
	}
	if pattern.Status != StatusDraft {
		t.Fatalf("status = %q after the failed promotion, want draft", pattern.Status)
	}

	// An OOS window exists but has too few trades.
	from := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 6, 0)
	pattern.Evidence = Evidence{
		DataFrom: from, DataTo: to, AsOf: to, Source: SourceBars,
		OOS:            backtest.Metrics{Trades: 5, From: from, To: to},
		DeflatedSharpe: 0.99, PBO: 0.1, Trials: 20, OOSTrades: 5, DataHash: "sha256:feed",
	}
	err := Promote(&pattern, PromotionPolicy{})
	if err == nil || !strings.Contains(err.Error(), "сдел") {
		t.Fatalf("error = %v, want the small OOS sample error", err)
	}
	if pattern.Status != StatusDraft {
		t.Fatalf("status = %q, want draft", pattern.Status)
	}

	// Complete OOS evidence passes the default policy.
	pattern.Evidence = Evidence{
		DataFrom: from, DataTo: to, AsOf: to, Source: SourceBars,
		OOS:            backtest.Metrics{Trades: 42, From: from, To: to},
		DeflatedSharpe: 0.97, PBO: 0.12, Trials: 48, OOSTrades: 42, DataHash: "sha256:deadbeef",
	}
	if err := Promote(&pattern, PromotionPolicy{}); err != nil {
		t.Fatalf("Promote(complete evidence): %v", err)
	}
	if pattern.Status != StatusValidated {
		t.Fatalf("status = %q, want validated", pattern.Status)
	}
	// Promoting an already validated pattern is a no-op.
	if err := Promote(&pattern, PromotionPolicy{}); err != nil {
		t.Fatalf("second Promote: %v", err)
	}
	// A retired pattern is never promoted again.
	pattern.Status = StatusRetired
	if err := Promote(&pattern, PromotionPolicy{}); err == nil {
		t.Fatal("a retired pattern must not be promoted")
	}
}

// TestSkillHasNoSecretsOrAccounts is the plan security test: the writer never
// picks up credentials or account numbers from the environment.
func TestSkillHasNoSecretsOrAccounts(t *testing.T) {
	login := "TESTLOGIN-SECRET"
	password := "TESTPASSWORD-SECRET"
	account := "TESTACCOUNT-SECRET"
	t.Setenv("TC_LOGIN", login)
	t.Setenv("TC_PASSWORD", password)
	t.Setenv("TC_ACCOUNT", account)
	// Extra guards: the common names some code reads by mistake.
	t.Setenv("TC_CLIENT", account)

	dir := t.TempDir()
	pattern := testPattern()
	path, err := WriteDraft(dir, pattern)
	if err != nil {
		t.Fatalf("WriteDraft: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	content := string(raw)
	for _, secret := range []string{login, password, account} {
		if strings.Contains(content, secret) {
			t.Fatalf("the skill file leaks %q:\n%s", secret, content)
		}
	}
	// The SKILL must state the paper-only limit explicitly.
	if !strings.Contains(content, "бумажн") {
		t.Fatalf("the skill file does not state that it is paper-only:\n%s", content)
	}
}

// TestWriteDraftRejectsUnsafeID guards the directory layout: only a slug id is
// allowed, otherwise a draft could escape the skills directory.
func TestWriteDraftRejectsUnsafeID(t *testing.T) {
	for _, id := range []string{"", "../evil", "/tmp/evil", "a/b", "RSI SBER", "rsi:sber"} {
		pattern := testPattern()
		pattern.ID = id
		if _, err := WriteDraft(t.TempDir(), pattern); err == nil {
			t.Fatalf("WriteDraft accepted the unsafe id %q", id)
		}
	}
}

// TestEvidenceFromSources checks the conversions of the research results into
// the auditable evidence block.
func TestEvidenceFromSources(t *testing.T) {
	from := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 6, 0)
	asOf := to.Add(time.Hour)
	walk := research.WalkForwardResult{
		OOS:            backtest.Metrics{Trades: 40, From: from, To: to},
		IS:             backtest.Metrics{Trades: 120, From: from.AddDate(0, -1, 0), To: to},
		DeflatedSharpe: 0.97,
		PBO:            0.11,
		Trials:         64,
	}
	evidence := EvidenceFromWalkForward(walk, asOf, "sha256:bars")
	if evidence.Source != SourceBars || evidence.OOSTrades != 40 || evidence.Trials != 64 || evidence.DataHash != "sha256:bars" {
		t.Fatalf("walk-forward evidence = %+v", evidence)
	}
	if !evidence.AsOf.Equal(asOf) || !evidence.DataFrom.Equal(walk.IS.From) || !evidence.DataTo.Equal(to) {
		t.Fatalf("walk-forward evidence times = %+v", evidence)
	}
	lob := lobreplay.Result{Metrics: backtest.Metrics{Trades: 12, From: from, To: from.AddDate(0, 0, 10)}}
	lobEvidence := EvidenceFromLOB(lob, asOf, "sha256:lob")
	if lobEvidence.Source != SourceLOB || lobEvidence.OOSTrades != 12 || !lobEvidence.DataTo.Equal(lob.Metrics.To) {
		t.Fatalf("LOB evidence = %+v", lobEvidence)
	}
}

// TestListReturnsDrafts checks the list_skill_patterns catalog helper: the
// drafts are listed with their status and title, stray files are ignored and a
// missing directory is an empty catalog.
func TestListReturnsDrafts(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteDraft(dir, testPattern()); err != nil {
		t.Fatalf("WriteDraft(draft): %v", err)
	}
	validated := testPattern()
	validated.ID = "rsi-validated"
	validated.Status = StatusValidated
	if _, err := WriteDraft(dir, validated); err != nil {
		t.Fatalf("WriteDraft(validated): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("stray file: %v", err)
	}
	summaries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %+v, want 2 patterns", summaries)
	}
	byID := map[string]Summary{}
	for _, summary := range summaries {
		byID[summary.ID] = summary
		if summary.Title == "" || summary.Path == "" || summary.ModifiedAt.IsZero() {
			t.Fatalf("incomplete summary = %+v", summary)
		}
	}
	if got := byID[testPattern().ID].Status; got != StatusDraft {
		t.Fatalf("draft status = %q", got)
	}
	if got := byID["rsi-validated"].Status; got != StatusValidated {
		t.Fatalf("validated status = %q", got)
	}
	if got := byID[testPattern().ID].Title; got != testPattern().Title {
		t.Fatalf("title = %q, want %q", got, testPattern().Title)
	}
	empty, err := List(filepath.Join(dir, "missing"))
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing directory = %+v, %v; want an empty catalog", empty, err)
	}
}

// TestMarketContextConditions checks the market-context lines the draft tool
// appends from the volatility oracle and the execution fingerprints.
func TestMarketContextConditions(t *testing.T) {
	lines := MarketContext(volatility.Forecast{Regime: "event", Multiplier: 2.4, Horizon: 8 * time.Hour}, []fingerprint.Footprint{
		{Kind: "twap", Side: "buy", Confidence: 0.8},
		{Kind: "unknown_algo"},
	})
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want the oracle line and the twap line", lines)
	}
	if !strings.Contains(lines[0], "event") || !strings.Contains(lines[0], "2.40") {
		t.Fatalf("oracle line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "twap") || !strings.Contains(lines[1], "0.80") {
		t.Fatalf("footprint line = %q", lines[1])
	}
}
