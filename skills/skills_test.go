// Package skills holds the repository skill files (plan Tasks 19/25): the
// SKILL.md frontmatter must stay valid for both DeepSeek Harness and the
// portable agent-skills format installed with "npx skills add", never look like
// a leaked token and only reference scripts that exist and are executable in
// the repository.
package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// tokenLikeRE matches long base64/hex-like runs: a leaked API token, a
	// password or an account number would trip it.
	tokenLikeRE = regexp.MustCompile("[A-Za-z0-9_-]{32,}")
	// scriptRefRE matches a repository-relative or skill-relative script path
	// mentioned in a SKILL.md body ("scripts/terminal.sh",
	// "trading-terminal/scripts/terminal.sh").
	scriptRefRE = regexp.MustCompile("(?:[A-Za-z0-9_.-]+/)*scripts/[A-Za-z0-9_.-]+")
)

// tokenLikeFindings returns the token-like strings of a SKILL.md body. Long
// snake_case identifiers (a lowercase/digit/'_' run with an underscore) are
// vocabulary: tool names such as terminal_bot_create_from_backtest are longer
// than a token but are not secrets, so they are skipped. Hex/base64 secrets keep
// mixed case or digits without underscores and are still reported.
func tokenLikeFindings(content string) []string {
	findings := make([]string, 0, 1)
	for _, token := range tokenLikeRE.FindAllString(content, -1) {
		if isSnakeCaseIdentifier(token) {
			continue
		}
		findings = append(findings, token)
	}
	return findings
}

// isSnakeCaseIdentifier reports whether token is a lowercase snake_case word
// (letters, digits and underscores, at least one underscore).
func isSnakeCaseIdentifier(token string) bool {
	if !strings.Contains(token, "_") {
		return false
	}
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// maxDescription is the frozen frontmatter limit of the plan.
const maxDescription = 1024

// TestSkillsFrontmatterValid is the plan Task 25 test: each skills/<dir>/SKILL.md
// carries the DSH/npx frontmatter (name equals the directory, a description
// shorter than 1024 characters), never contains a token-like string and only
// mentions existing executable scripts; the portable REST entry point and the
// installation README are present.
func TestSkillsFrontmatterValid(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the skills directory: %v", err)
	}
	found := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := entry.Name()
		path := filepath.Join(dir, "SKILL.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			continue // not a skill directory
		}
		found++
		content := string(raw)
		frontmatter := parseFrontmatter(t, path, content)
		if got := frontmatter["name"]; got != dir {
			t.Errorf("%s: name = %q, want the directory name %q", path, got, dir)
		}
		description := frontmatter["description"]
		if description == "" {
			t.Errorf("%s: empty description", path)
		}
		if len(description) >= maxDescription {
			t.Errorf("%s: description is %d chars, want < %d", path, len(description), maxDescription)
		}
		if findings := tokenLikeFindings(content); len(findings) > 0 {
			t.Errorf("%s: token-like string %q (secrets belong in .env, never in a SKILL)", path, findings[0])
		}
		for _, reference := range scriptRefRE.FindAllString(content, -1) {
			scriptPath := filepath.Join(dir, reference)
			if _, statErr := os.Stat(scriptPath); statErr != nil {
				scriptPath = filepath.Join(".", reference)
				if _, statErr = os.Stat(scriptPath); statErr != nil {
					t.Errorf("%s: referenced script %q does not exist", path, reference)
					continue
				}
			}
			info, statErr := os.Stat(scriptPath)
			if statErr != nil {
				continue
			}
			if info.Mode()&0o111 == 0 {
				t.Errorf("%s: referenced script %s is not executable", path, scriptPath)
			}
		}
	}
	if found < 2 {
		t.Fatalf("found %d skills, want at least the two repository skills", found)
	}

	// The portable REST entry point of the terminal skill must exist and be runnable.
	script := filepath.Join("trading-terminal", "scripts", "terminal.sh")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("the portable terminal script %s is missing: %v", script, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("the portable terminal script %s is not executable (mode %v)", script, info.Mode())
	}

	// The skills README documents the one-command installation.
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("skills/README.md is missing: %v", err)
	}
	if !strings.Contains(string(readme), "npx skills add") {
		t.Fatalf("skills/README.md does not document 'npx skills add'")
	}
}

// parseFrontmatter reads the flat top-level scalars of the leading --- block.
func parseFrontmatter(t *testing.T, path, content string) map[string]string {
	t.Helper()
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		t.Fatalf("%s: no leading --- frontmatter fence", path)
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatalf("%s: unterminated frontmatter", path)
	}
	values := map[string]string{}
	for _, line := range lines[1:end] {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		values[key] = unquoteScalar(value)
	}
	return values
}

// unquoteScalar strips one pair of matching YAML quotes.
func unquoteScalar(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// TestTokenLikeFindingsDistinguishesSecretsFromToolNames is the regression for
// the token-like scan: long snake_case tool names are vocabulary, while mixed
// hex/base64 runs are still reported as possible secrets.
func TestTokenLikeFindingsDistinguishesSecretsFromToolNames(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "tool name", content: "вызови terminal_bot_create_from_backtest с id бота", want: false},
		{name: "tool names in a list", content: "terminal_bot_promote, terminal_bot_create_from_backtest", want: false},
		{name: "hex secret", content: "token: 9f8e7d6c5b4a39281706f5e4d3c2b1a09988776655443322", want: true},
		{name: "base64 secret", content: "key=Q2hhbmdlTWVQbGVhc2VUaGlzSXNBU2VjcmV0MTIzNDU2Nzg5MA", want: true},
		{name: "mixed base64 secret", content: "Zx9Qw8Er7Ty6Ui5Op4As3Df2Gh1Jk0Lm", want: true},
		{name: "underscore secret with uppercase", content: "AbCdEfGhIjKlMnOpQrStUvWxYz012345_6789", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := tokenLikeFindings(tc.content)
			if tc.want && len(findings) == 0 {
				t.Fatalf("tokenLikeFindings(%q) = none, want a secret", tc.content)
			}
			if !tc.want && len(findings) != 0 {
				t.Fatalf("tokenLikeFindings(%q) = %v, want none", tc.content, findings)
			}
		})
	}
}
