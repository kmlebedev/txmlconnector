package automation

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// hotspot builds one repeated tool sequence for the backlog tests.
func hotspot(sequence []string, calls, tokens int) Hotspot {
	return Hotspot{
		Sequence: sequence,
		Calls:    calls,
		Tokens:   tokens,
		Example:  map[string]any{"symbol": "TQBR:SBER", "periodSec": 300},
	}
}

func automationKinds() map[string]bool {
	return map[string]bool{
		KindGoFunction:  true,
		KindMCPTool:     true,
		KindStaticModel: true,
		KindMLModel:     true,
		KindSchedule:    true,
	}
}

// TestProposeFromHotspotsRanksByTokens is the plan Task 20 test: expensive
// repeated workflows become automation candidates, ranked by the tokens they
// cost per week, and cheap ones can be filtered out.
func TestProposeFromHotspotsRanksByTokens(t *testing.T) {
	hotspots := []Hotspot{
		hotspot([]string{"get_quote"}, 5, 100),
		hotspot([]string{"walk_forward", "get_market_hints"}, 3, 900),
		hotspot([]string{"get_history", "run_backtest"}, 10, 400),
		hotspot([]string{"get_quote", "get_portfolio"}, 2, 50),
	}
	// Deliberately unsorted input: the result must be ordered by the estimate.
	tasks := ProposeFromHotspots(hotspots, 0)
	if len(tasks) != len(hotspots) {
		t.Fatalf("tasks = %d, want %d", len(tasks), len(hotspots))
	}
	for index := 1; index < len(tasks); index++ {
		if tasks[index].EstTokensSavedPerWeek > tasks[index-1].EstTokensSavedPerWeek {
			t.Fatalf("tasks are not ranked by estimated tokens: %+v", tasks)
		}
	}
	if !reflect.DeepEqual(tasks[0].Hotspot.Sequence, []string{"walk_forward", "get_market_hints"}) {
		t.Fatalf("ranked[0] = %v, want the 900-token workflow", tasks[0].Hotspot.Sequence)
	}
	if tasks[0].EstTokensSavedPerWeek != 900 {
		t.Fatalf("estimate = %d, want 900", tasks[0].EstTokensSavedPerWeek)
	}

	filtered := ProposeFromHotspots(hotspots, 500)
	if len(filtered) != 1 || !reflect.DeepEqual(filtered[0].Hotspot.Sequence, tasks[0].Hotspot.Sequence) {
		t.Fatalf("minTokens filter = %+v, want only the 900-token workflow", filtered)
	}
	if len(ProposeFromHotspots(nil, 0)) != 0 {
		t.Fatal("an empty hotspot list produced tasks")
	}

	seen := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		if task.ID == "" || !strings.HasPrefix(task.ID, "auto-") {
			t.Fatalf("task ID = %q, want the auto- prefix", task.ID)
		}
		if seen[task.ID] {
			t.Fatalf("duplicate task ID %q", task.ID)
		}
		seen[task.ID] = true
		if !automationKinds()[task.Kind] {
			t.Fatalf("%s: kind = %q", task.ID, task.Kind)
		}
		if task.Status != StatusProposed {
			t.Fatalf("%s: status = %q, want proposed", task.ID, task.Status)
		}
		if task.Title == "" || task.Rationale == "" {
			t.Fatalf("%s: empty title/rationale: %+v", task.ID, task)
		}
		if !strings.Contains(task.Spec, "Files:") || !strings.Contains(task.Spec, "Interfaces:") ||
			!strings.Contains(task.Spec, "Тест:") {
			t.Fatalf("%s: spec is not a mini-plan: %q", task.ID, task.Spec)
		}
	}

	// The same hotspots must produce the same plan (IDs and order included).
	if again := ProposeFromHotspots(hotspots, 0); !reflect.DeepEqual(tasks, again) {
		t.Fatal("ProposeFromHotspots is not deterministic")
	}
}

// TestWriteBacklogIsIdempotent: writing the same tasks twice keeps one copy,
// duplicate IDs inside one call are collapsed, and a repeated write of a
// subset does not duplicate the already written tasks.
func TestWriteBacklogIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "automation-backlog.md")
	tasks := ProposeFromHotspots([]Hotspot{
		hotspot([]string{"walk_forward", "get_market_hints"}, 3, 900),
		hotspot([]string{"get_history", "run_backtest"}, 10, 400),
	}, 0)
	if len(tasks) != 2 {
		t.Fatalf("fixture tasks = %d, want 2", len(tasks))
	}
	if err := WriteBacklog(path, tasks); err != nil {
		t.Fatalf("WriteBacklog: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backlog: %v", err)
	}
	if err := WriteBacklog(path, tasks); err != nil {
		t.Fatalf("WriteBacklog (second): %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backlog: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("repeated write changed the file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	for _, task := range tasks {
		if count := strings.Count(string(second), "### `"+task.ID+"`"); count != 1 {
			t.Fatalf("task %s appears %d times, want 1", task.ID, count)
		}
	}

	// Duplicate tasks in one call are collapsed by ID.
	duplicated := append(append([]AutomationTask(nil), tasks...), tasks...)
	if err := WriteBacklog(path, duplicated); err != nil {
		t.Fatalf("WriteBacklog (duplicates): %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backlog: %v", err)
	}
	for _, task := range tasks {
		if count := strings.Count(string(data), "### `"+task.ID+"`"); count != 1 {
			t.Fatalf("duplicated input: task %s appears %d times, want 1", task.ID, count)
		}
	}

	// Repeated write of a subset keeps the other generated tasks: they are not
	// duplicated, only preserved.
	if err := WriteBacklog(path, tasks[:1]); err != nil {
		t.Fatalf("WriteBacklog (subset): %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backlog: %v", err)
	}
	for _, task := range tasks {
		if count := strings.Count(string(data), "### `"+task.ID+"`"); count != 1 {
			t.Fatalf("subset write: task %s appears %d times, want 1", task.ID, count)
		}
	}
}

// TestBacklogPreservesManualText: text outside the auto markers is never
// touched, and the generated block replaces only the old generated block.
func TestBacklogPreservesManualText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "automation-backlog.md")
	before := "# Бэклог автоматизации\n\nРучной текст до блока.\n\n"
	after := "\n\nРучной текст после блока.\n"
	oldBlock := "<!-- auto:begin -->\nстарая сгенерированная задача\n<!-- auto:end -->"
	if err := os.WriteFile(path, []byte(before+oldBlock+after), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tasks := ProposeFromHotspots([]Hotspot{hotspot([]string{"walk_forward"}, 3, 900)}, 0)
	if err := WriteBacklog(path, tasks); err != nil {
		t.Fatalf("WriteBacklog: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backlog: %v", err)
	}
	got := string(data)
	if !strings.HasPrefix(got, before) {
		t.Fatalf("manual prefix changed:\n%s", got)
	}
	if !strings.HasSuffix(got, after) {
		t.Fatalf("manual suffix changed:\n%s", got)
	}
	if strings.Contains(got, "старая сгенерированная задача") {
		t.Fatal("old generated block is still present")
	}
	if strings.Count(got, "<!-- auto:begin -->") != 1 || strings.Count(got, "<!-- auto:end -->") != 1 {
		t.Fatalf("auto markers are duplicated: %s", got)
	}
	if !strings.Contains(got, tasks[0].ID) || !strings.Contains(got, tasks[0].Title) {
		t.Fatalf("task is missing from the backlog:\n%s", got)
	}
}
