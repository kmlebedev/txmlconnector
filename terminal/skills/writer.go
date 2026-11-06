package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// skillFileName is the file name of one pattern inside its directory.
const skillFileName = "SKILL.md"

// patternIDRE is the allowed pattern id: a lowercase hyphen slug, so a draft can
// never escape <dir>/<id>/ and the skill name is stable for the loader.
var patternIDRE = regexp.MustCompile("^[a-z0-9][a-z0-9-]{0,63}$")

// Summary is the catalog entry of one pattern on disk (list_skill_patterns).
type Summary struct {
	ID         string
	Title      string
	Status     string
	Path       string
	ModifiedAt time.Time
}

// WriteDraft writes the pattern as <dir>/<id>/SKILL.md with the Harness
// frontmatter (name, description, whenToUse, metadata.status,
// metadata.evidence) and returns the file path. A missing Status is written as
// draft. The id must be a lowercase hyphen slug.
func WriteDraft(dir string, p Pattern) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("skills: the skills directory is required")
	}
	id := strings.TrimSpace(p.ID)
	if !patternIDRE.MatchString(id) {
		return "", fmt.Errorf("skills: pattern id %q must be a lowercase hyphen slug ([a-z0-9-], 1..64)", p.ID)
	}
	status := p.Status
	if status == "" {
		status = StatusDraft
	}
	switch status {
	case StatusDraft, StatusValidated, StatusRetired:
	default:
		return "", fmt.Errorf("skills: unknown pattern status %q", p.Status)
	}
	p.ID, p.Status = id, status
	target := filepath.Join(dir, id, skillFileName)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("skills: create the pattern directory: %w", err)
	}
	if err := os.WriteFile(target, []byte(renderSkill(p)), 0o644); err != nil {
		return "", fmt.Errorf("skills: write %s: %w", target, err)
	}
	return target, nil
}

// renderSkill renders the full SKILL.md of a pattern: the Harness frontmatter
// plus the human-readable body with the evidence and the paper-only limits.
func renderSkill(p Pattern) string {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("name: " + p.ID + "\n")
	builder.WriteString("description: " + yamlString(descriptionOf(p)) + "\n")
	builder.WriteString("whenToUse: " + yamlString(whenToUse) + "\n")
	builder.WriteString("metadata:\n")
	builder.WriteString("  status: " + p.Status + "\n")
	builder.WriteString("  evidence:\n")
	builder.WriteString("    dataFrom: " + yamlString(chTime(p.Evidence.DataFrom)) + "\n")
	builder.WriteString("    dataTo: " + yamlString(chTime(p.Evidence.DataTo)) + "\n")
	builder.WriteString("    asOf: " + yamlString(chTime(p.Evidence.AsOf)) + "\n")
	builder.WriteString("    source: " + yamlString(p.Evidence.Source) + "\n")
	builder.WriteString("    trials: " + strconv.Itoa(p.Evidence.Trials) + "\n")
	builder.WriteString("    oosTrades: " + strconv.Itoa(p.Evidence.OOSTradeCount()) + "\n")
	builder.WriteString("    deflatedSharpe: " + strconv.FormatFloat(p.Evidence.DeflatedSharpe, 'f', 4, 64) + "\n")
	builder.WriteString("    pbo: " + strconv.FormatFloat(p.Evidence.PBO, 'f', 4, 64) + "\n")
	builder.WriteString("    dataHash: " + yamlString(p.Evidence.DataHash) + "\n")
	builder.WriteString("---\n\n")

	builder.WriteString("# " + strings.TrimSpace(p.Title) + "\n\n")
	builder.WriteString("## Гипотеза\n\n" + emptyText(p.Hypothesis) + "\n\n")
	builder.WriteString("## Инструменты\n\n" + bulletList(p.Instruments, "универсум терминала") + "\n")
	builder.WriteString("## Условия\n\n" + bulletList(p.Conditions, "условия не описаны") + "\n")
	builder.WriteString("## Вход\n\n" + emptyText(p.Entry) + "\n\n")
	builder.WriteString("## Выход\n\n" + emptyText(p.Exit) + "\n\n")
	builder.WriteString("## Риск\n\n" + emptyText(p.Risk) + "\n\n")
	builder.WriteString("## Доказательства (out-of-sample)\n\n")
	builder.WriteString("- Источник: " + emptyText(p.Evidence.Source) + "\n")
	builder.WriteString("- Данные: " + timeRange(p.Evidence.DataFrom, p.Evidence.DataTo) + ", asOf " + emptyText(chTime(p.Evidence.AsOf)) + "\n")
	builder.WriteString("- OOS-сделки: " + strconv.Itoa(p.Evidence.OOSTradeCount()) + "\n")
	builder.WriteString("- Deflated Sharpe: " + strconv.FormatFloat(p.Evidence.DeflatedSharpe, 'f', 4, 64) +
		", PBO: " + strconv.FormatFloat(p.Evidence.PBO, 'f', 4, 64) +
		", испытаний: " + strconv.Itoa(p.Evidence.Trials) + "\n")
	builder.WriteString("- OOS-метрики: доходность " + strconv.FormatFloat(p.Evidence.OOS.TotalReturnPct, 'f', 2, 64) +
		"%, просадка " + strconv.FormatFloat(p.Evidence.OOS.MaxDrawdownPct, 'f', 2, 64) +
		"%, Sharpe " + strconv.FormatFloat(p.Evidence.OOS.Sharpe, 'f', 2, 64) +
		", win rate " + strconv.FormatFloat(p.Evidence.OOS.WinRatePct, 'f', 2, 64) + "%\n")
	builder.WriteString("- Окно OOS: " + timeRange(p.Evidence.OOS.From, p.Evidence.OOS.To) + "\n")
	builder.WriteString("- Отпечаток данных: " + emptyText(p.Evidence.DataHash) + "\n\n")
	builder.WriteString("## Ограничения\n\n")
	builder.WriteString("- Только бумажная торговля: навык не включает live-исполнение и не маршрутизирует реальные заявки.\n")
	builder.WriteString("- Без гарантий доходности: прошлые результаты не повторятся, а OOS-метрики — не обещание.\n")
	builder.WriteString("- In-sample и out-of-sample проверка не заменяют риск-менеджмент: лимиты обязательны.\n")
	builder.WriteString("- Счёт, логин, пароль и любые секреты в SKILL не попадают; в репозиторий паттерн попадает только через PR человека.\n\n")
	builder.WriteString("## Статус\n\n")
	builder.WriteString("_" + p.Status + "_ — validated делает навык видимым агенту, но не включает live; ")
	builder.WriteString("черновик не считается доказанным паттерном до promotion по evidence policy.\n")
	return builder.String()
}

// whenToUse is the Harness matching hint of the authoring skill.
const whenToUse = "Use when drafting, reviewing or promoting a trading-pattern SKILL from walk-forward, order-book replay, volatility-oracle or execution-fingerprint evidence."

// descriptionOf builds the single-line Harness description of the pattern.
func descriptionOf(p Pattern) string {
	instruments := "the terminal universe"
	if len(p.Instruments) > 0 {
		instruments = strings.Join(p.Instruments, ", ")
	}
	hypothesis := singleLine(p.Hypothesis)
	if len(hypothesis) > 240 {
		hypothesis = hypothesis[:240] + "…"
	}
	description := "Use when the user asks about the trading pattern \"" + strings.TrimSpace(p.Title) + "\" (" + instruments + ")"
	if hypothesis != "" {
		description += ": " + hypothesis
	}
	return description + " Paper-only research skill backed by out-of-sample evidence; it never routes live orders."
}

// yamlString renders one single-line double-quoted YAML scalar.
func yamlString(value string) string {
	return strconv.Quote(singleLine(value))
}

// singleLine collapses all whitespace and control characters into single spaces,
// so a YAML scalar and the SKILL body stay one line each.
func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// chTime renders a point-in-time instant as RFC3339 UTC; the zero time is
// rendered as an empty string.
func chTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// timeRange renders the from..to window of the evidence.
func timeRange(from, to time.Time) string {
	if from.IsZero() && to.IsZero() {
		return "не указано"
	}
	return chTime(from) + ".." + chTime(to)
}

// bulletList renders a markdown bullet list with a fallback line.
func bulletList(items []string, fallback string) string {
	var builder strings.Builder
	for _, item := range items {
		item = singleLine(item)
		if item == "" {
			continue
		}
		builder.WriteString("- " + item + "\n")
	}
	if builder.Len() == 0 {
		return "- " + fallback + "\n"
	}
	return builder.String()
}

// emptyText renders a required body text with a fallback.
func emptyText(value string) string {
	if strings.TrimSpace(value) == "" {
		return "не задано"
	}
	return strings.TrimSpace(value)
}

// List scans dir for pattern drafts and returns their catalog summaries ordered
// by id. A missing directory is an empty catalog, not an error.
func List(dir string) ([]Summary, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("skills: the skills directory is required")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("skills: read the skills directory: %w", err)
	}
	summaries := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !patternIDRE.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name(), skillFileName)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("skills: read %s: %w", path, err)
		}
		meta, err := parseSkillFile(string(raw))
		if err != nil {
			return nil, fmt.Errorf("skills: parse %s: %w", path, err)
		}
		title := meta.Title
		if title == "" {
			title = entry.Name()
		}
		summaries = append(summaries, Summary{
			ID:         entry.Name(),
			Title:      title,
			Status:     meta.Status,
			Path:       path,
			ModifiedAt: info.ModTime().UTC(),
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].ID < summaries[j].ID })
	return summaries, nil
}

// skillFileMeta is the subset of a SKILL.md the catalog and the tools need.
type skillFileMeta struct {
	Name        string
	Description string
	WhenToUse   string
	Status      string
	Title       string
	Evidence    map[string]string
}

// parseSkillFile reads the Harness frontmatter of a SKILL.md back into the
// metadata the tools need (status, evidence, title). It mirrors the loader the
// tests use: the block between the --- fences with two metadata levels.
func parseSkillFile(raw string) (skillFileMeta, error) {
	meta := skillFileMeta{Evidence: map[string]string{}}
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return meta, errors.New("skills: no frontmatter fence")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return meta, errors.New("skills: unterminated frontmatter")
	}
	for _, line := range lines[1:end] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), unquoteYAML(strings.TrimSpace(value))
		switch indent {
		case 0:
			// Top-level keys: the section headers ("metadata:") carry no value.
			switch key {
			case "name":
				meta.Name = value
			case "description":
				meta.Description = value
			case "whenToUse":
				meta.WhenToUse = value
			}
		case 2:
			// The first metadata level: status plus the "evidence:" header.
			if key == "status" {
				meta.Status = value
			}
		case 4:
			// The evidence block scalars.
			meta.Evidence[key] = value
		}
	}
	for _, line := range lines[end+1:] {
		if strings.HasPrefix(line, "# ") {
			meta.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			break
		}
	}
	return meta, nil
}

// unquoteYAML strips the surrounding single/double quotes of a scalar and
// undoes the strconv.Quote escapes of yamlString.
func unquoteYAML(value string) string {
	if len(value) < 2 {
		return value
	}
	if !((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value
	}
	inner := value[1 : len(value)-1]
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return inner
}
