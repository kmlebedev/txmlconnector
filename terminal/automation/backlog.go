package automation

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Automation task kinds (plan Task 20).
const (
	KindGoFunction  = "go_function"
	KindMCPTool     = "mcp_tool"
	KindStaticModel = "static_model"
	KindMLModel     = "ml_model"
	KindSchedule    = "schedule"
)

// Statuses of one automation task.
const (
	StatusProposed = "proposed"
	StatusAccepted = "accepted"
	StatusDone     = "done"
)

// BacklogPath is the repository backlog of automation tasks.
const BacklogPath = "docs/ai-terminal/automation-backlog.md"

// The generated block of the backlog is delimited by these markers; everything
// outside them is manual text and is never rewritten.
const (
	backlogBeginMarker = "<!-- auto:begin -->"
	backlogEndMarker   = "<!-- auto:end -->"
	backlogHeader      = "# Бэклог автоматизации вычислений\n"
)

// AutomationTask is one reasoned proposal to replace a repeated LLM workflow
// with a deterministic computation (plan Task 20). Spec is a mini-plan in the
// format of the repository plan: Files, Interfaces, тест.
type AutomationTask struct {
	ID                    string  `json:"id"`
	Title                 string  `json:"title"`
	Kind                  string  `json:"kind"`
	Rationale             string  `json:"rationale"`
	Hotspot               Hotspot `json:"hotspot"`
	EstTokensSavedPerWeek int     `json:"estTokensSavedPerWeek"`
	Spec                  string  `json:"spec"`
	Status                string  `json:"status"`
}

// ProposeFromHotspots turns the repeated tool sequences of the token ledger into
// automation tasks. Hotspots cheaper than minTokens are skipped; the result is
// ordered by the estimated weekly saving (largest first) and is deterministic.
//
// The ledger keeps no timestamps, so EstTokensSavedPerWeek assumes the whole
// recorded period is one week: it is a lower-bound estimate of the tokens the
// agent will not have to spend next week.
func ProposeFromHotspots(hotspots []Hotspot, minTokens int) []AutomationTask {
	if minTokens < 0 {
		minTokens = 0
	}
	tasks := make([]AutomationTask, 0, len(hotspots))
	seen := make(map[string]bool, len(hotspots))
	for _, spot := range hotspots {
		if len(spot.Sequence) == 0 || spot.Tokens < minTokens {
			continue
		}
		id := taskID(spot.Sequence)
		if seen[id] {
			continue
		}
		seen[id] = true
		kind, rationale := classifyHotspot(spot)
		tasks = append(tasks, AutomationTask{
			ID:                    id,
			Title:                 taskTitle(spot.Sequence),
			Kind:                  kind,
			Rationale:             rationale,
			Hotspot:               spot,
			EstTokensSavedPerWeek: spot.Tokens,
			Spec:                  taskSpec(spot, kind),
			Status:                StatusProposed,
		})
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].EstTokensSavedPerWeek != tasks[j].EstTokensSavedPerWeek {
			return tasks[i].EstTokensSavedPerWeek > tasks[j].EstTokensSavedPerWeek
		}
		if tasks[i].Hotspot.Tokens != tasks[j].Hotspot.Tokens {
			return tasks[i].Hotspot.Tokens > tasks[j].Hotspot.Tokens
		}
		return tasks[i].ID < tasks[j].ID
	})
	return tasks
}

// taskID derives a stable task id from the tool sequence: the same repeated
// workflow always gets the same id, so a repeated backlog write cannot
// duplicate it.
func taskID(sequence []string) string {
	hasher := fnv.New64a()
	for _, name := range sequence {
		_, _ = hasher.Write([]byte(name))
		_, _ = hasher.Write([]byte{0})
	}
	return fmt.Sprintf("auto-%016x", hasher.Sum64())
}

// taskTitle is the human title of the task.
func taskTitle(sequence []string) string {
	return "Автоматизировать последовательность: " + strings.Join(sequence, " → ")
}

// classifyHotspot picks the automation kind and the one-line rationale.
func classifyHotspot(spot Hotspot) (kind, rationale string) {
	names := spot.Sequence
	switch {
	case containsPrefix(names, "bot_") || containsAnyName(names, "start_autonomy", "stop_autonomy"):
		return KindSchedule, "Последовательность запускается или наблюдается периодически — кандидат " +
			"на расписание DSH вместо ручного повторения моделью"
	case containsAnyName(names, "detect_execution_algos", "vol_forecast"):
		return KindMLModel, "Сейчас прогноз или классификацию оркестрирует модель; офлайн-модель " +
			"(бустинг или квантильная регрессия) с инференсом через интерфейс даст тот же результат дешевле"
	case containsAnyName(names, "walk_forward", "run_backtest", "backtest_rules", "compare_strategies"):
		return KindGoFunction, "Детерминированный расчёт повторяется через модель: Go-функция " +
			"вернёт тот же результат без токенов"
	case containsAnyName(names, "get_market_hints"):
		return KindStaticModel, "Статистическая модель подсказок (тренд/RSI/ATR) считается по одним " +
			"и тем же правилам — её место в Go, а не в промпте"
	case containsAnyName(names, "get_history", "get_quote", "get_portfolio", "get_news", "vol_events", "list_instruments", "get_strategies"):
		return KindMCPTool, "Повторяющееся чтение данных терминала — готовый кандидат на MCP-инструмент " +
			"с фиксированной схемой аргументов"
	default:
		return KindGoFunction, "Повторяющаяся последовательность инструментов дешевле в виде Go-функции"
	}
}

// taskSpec builds the mini-plan of the task in the repository plan format.
func taskSpec(spot Hotspot, kind string) string {
	slug := taskSlug(spot.Sequence)
	camel := camelSlug(slug)
	files := fmt.Sprintf("terminal/research/%s.go, terminal/research/%s_test.go", slug, slug)
	workflow := strings.Join(spot.Sequence, " → ")
	var interfaces string
	switch kind {
	case KindMCPTool:
		interfaces = fmt.Sprintf("MCP-инструмент terminal_%s: вход — поля %s; выход — данные последовательности %s",
			slug, exampleFields(spot.Example), workflow)
	case KindSchedule:
		interfaces = fmt.Sprintf("Расписание DSH: периодический запуск %s; шаг и аргументы — из %s",
			workflow, exampleFields(spot.Example))
	case KindStaticModel, KindMLModel:
		interfaces = fmt.Sprintf("func %s(ctx context.Context, in map[string]any) (map[string]any, error): инференс модели без LLM; обучение и калибровка — офлайн", camel)
	default:
		interfaces = fmt.Sprintf("func %s(ctx context.Context, in map[string]any) (map[string]any, error): замена последовательности %s", camel, workflow)
	}
	return fmt.Sprintf("Files: %s\nInterfaces: %s\nТест: Test%sMatchesManualWorkflow (тот же результат, что у последовательности %s)",
		files, interfaces, camel, workflow)
}

// taskSlug turns the tool sequence into a file-name friendly slug.
func taskSlug(sequence []string) string {
	parts := make([]string, 0, len(sequence))
	for _, name := range sequence {
		var builder strings.Builder
		for _, char := range strings.ToLower(name) {
			if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
				builder.WriteRune(char)
			} else {
				builder.WriteByte('_')
			}
		}
		parts = append(parts, strings.Trim(builder.String(), "_"))
	}
	return strings.Join(parts, "_")
}

// camelSlug turns a slug into an exported Go identifier.
func camelSlug(slug string) string {
	parts := strings.FieldsFunc(slug, func(char rune) bool { return char == '_' })
	var builder strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		builder.WriteString(strings.ToUpper(part[:1]))
		builder.WriteString(part[1:])
	}
	if builder.Len() == 0 {
		return "Automation"
	}
	return builder.String()
}

// exampleFields renders the sorted argument names of the first call.
func exampleFields(example map[string]any) string {
	if len(example) == 0 {
		return "первого вызова"
	}
	keys := make([]string, 0, len(example))
	for key := range example {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// formatExample renders the first-call arguments as a stable sorted string.
func formatExample(example map[string]any) string {
	if len(example) == 0 {
		return ""
	}
	keys := make([]string, 0, len(example))
	for key := range example {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+fmt.Sprint(example[key]))
	}
	return strings.Join(parts, ", ")
}

// containsAnyName reports whether the sequence contains one of the names.
func containsAnyName(sequence []string, names ...string) bool {
	for _, name := range sequence {
		for _, want := range names {
			if name == want {
				return true
			}
		}
	}
	return false
}

// containsPrefix reports whether a sequence contains a tool starting with the
// prefix.
func containsPrefix(sequence []string, prefix string) bool {
	for _, name := range sequence {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// WriteBacklog writes the tasks into the generated block of the backlog file,
// preserving the manual text outside the markers. The generated block is merged
// by task ID: tasks already in the file are never duplicated; their Status
// (accepted/done) is preserved on a rewrite, and generated tasks that are not
// part of this call are kept.
func WriteBacklog(path string, tasks []AutomationTask) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("automation: backlog path is required")
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("automation: read backlog %s: %w", path, err)
	}
	content := string(existing)
	prefix, body, suffix, hasMarkers, splitErr := splitBacklog(content)
	if splitErr != nil {
		return splitErr
	}
	oldBlocks := parseTaskBlocks(body)
	merged := mergeTasks(tasks, oldBlocks)
	region := renderBacklogRegion(merged)
	var out string
	if hasMarkers {
		out = prefix + region + suffix
	} else {
		base := strings.TrimRight(prefix, "\n")
		if base == "" {
			base = strings.TrimRight(backlogHeader, "\n")
		}
		out = base + "\n\n" + region + "\n"
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("automation: create backlog directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return fmt.Errorf("automation: write backlog %s: %w", path, err)
	}
	return nil
}

// splitBacklog separates the manual prefix, the generated body and the manual
// suffix of the backlog.
func splitBacklog(content string) (prefix, body, suffix string, hasMarkers bool, err error) {
	begin := strings.Index(content, backlogBeginMarker)
	end := strings.Index(content, backlogEndMarker)
	if begin == -1 && end == -1 {
		return content, "", "", false, nil
	}
	if begin == -1 || end == -1 || end < begin {
		return "", "", "", false, fmt.Errorf("automation: backlog auto markers are incomplete or out of order")
	}
	return content[:begin], content[begin+len(backlogBeginMarker) : end], content[end+len(backlogEndMarker):], true, nil
}

// parsedBlock is one task block already present in the generated section.
type parsedBlock struct {
	id   string
	text string
}

// parseTaskBlocks parses the generated task sections in file order.
func parseTaskBlocks(body string) []parsedBlock {
	blocks := make([]parsedBlock, 0, 4)
	var currentID string
	var current []string
	flush := func() {
		if currentID == "" {
			return
		}
		blocks = append(blocks, parsedBlock{id: currentID, text: strings.Join(current, "\n")})
	}
	for _, line := range strings.Split(body, "\n") {
		if id := parseBlockID(line); id != "" {
			flush()
			currentID = id
			current = []string{line}
			continue
		}
		if currentID != "" {
			current = append(current, line)
		}
	}
	flush()
	return blocks
}

// parseBlockID extracts the task ID from a task heading line.
func parseBlockID(line string) string {
	trimmed := strings.TrimSpace(line)
	const headingPrefix = "### `"
	if !strings.HasPrefix(trimmed, headingPrefix) {
		return ""
	}
	rest := trimmed[len(headingPrefix):]
	end := strings.Index(rest, "`")
	if end <= 0 {
		return ""
	}
	id := rest[:end]
	if !strings.HasPrefix(id, "auto-") {
		return ""
	}
	return id
}

// mergeItem is one rendered or preserved task block of the merged backlog.
type mergeItem struct {
	id   string
	task *AutomationTask
	text string
}

// mergeTasks merges the proposed tasks with the blocks already in the file: new
// tasks win by ID (with the manual Status preserved), the other generated tasks
// are kept verbatim.
func mergeTasks(tasks []AutomationTask, old []parsedBlock) []mergeItem {
	oldByID := make(map[string]parsedBlock, len(old))
	for _, block := range old {
		if _, exists := oldByID[block.id]; !exists {
			oldByID[block.id] = block
		}
	}
	ordered := make([]AutomationTask, 0, len(tasks))
	seen := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		if task.ID == "" || seen[task.ID] {
			continue
		}
		seen[task.ID] = true
		if task.Status == "" {
			task.Status = StatusProposed
		}
		if existing, ok := oldByID[task.ID]; ok {
			if status := blockStatus(existing.text); status == StatusAccepted || status == StatusDone {
				task.Status = status
			}
		}
		ordered = append(ordered, task)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].EstTokensSavedPerWeek != ordered[j].EstTokensSavedPerWeek {
			return ordered[i].EstTokensSavedPerWeek > ordered[j].EstTokensSavedPerWeek
		}
		return ordered[i].ID < ordered[j].ID
	})
	items := make([]mergeItem, 0, len(ordered)+len(old))
	for index := range ordered {
		task := ordered[index]
		items = append(items, mergeItem{id: task.ID, task: &task})
	}
	for _, block := range old {
		if seen[block.id] {
			continue
		}
		items = append(items, mergeItem{id: block.id, text: block.text})
	}
	return items
}

// blockStatus reads the Status field of an already rendered task block.
func blockStatus(block string) string {
	const marker = "Status: `"
	index := strings.Index(block, marker)
	if index < 0 {
		return ""
	}
	rest := block[index+len(marker):]
	end := strings.Index(rest, "`")
	if end <= 0 {
		return ""
	}
	return rest[:end]
}

// renderBacklogRegion renders the whole generated block, markers included.
func renderBacklogRegion(items []mergeItem) string {
	var body strings.Builder
	body.WriteString("\n\n")
	if len(items) == 0 {
		body.WriteString("_Пока нет предложенных задач: повторяемых последовательностей с достаточной стоимостью не найдено._\n\n")
	} else {
		body.WriteString("_Сгенерировано автоматически из учёта токенов агента; вручную редактируйте только вне маркеров._\n\n")
		for _, item := range items {
			if item.task != nil {
				body.WriteString(renderTaskBlock(*item.task))
			} else {
				body.WriteString(strings.TrimSpace(item.text))
			}
			body.WriteString("\n\n")
		}
	}
	return backlogBeginMarker + body.String() + backlogEndMarker
}

// renderTaskBlock renders one task section of the generated block.
func renderTaskBlock(task AutomationTask) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "### `%s` — %s\n\n", task.ID, task.Title)
	fmt.Fprintf(&builder, "- Kind: `%s`; Status: `%s`; Calls: %d; Tokens: %d; EstTokensSavedPerWeek: %d\n",
		task.Kind, task.Status, task.Hotspot.Calls, task.Hotspot.Tokens, task.EstTokensSavedPerWeek)
	fmt.Fprintf(&builder, "- Rationale: %s\n", task.Rationale)
	if len(task.Hotspot.Sequence) > 0 {
		fmt.Fprintf(&builder, "- Hotspot: `%s`", strings.Join(task.Hotspot.Sequence, " → "))
		if example := formatExample(task.Hotspot.Example); example != "" {
			fmt.Fprintf(&builder, "; пример аргументов: `%s`", example)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\n**Spec:**\n\n```text\n")
	builder.WriteString(strings.TrimSpace(task.Spec))
	builder.WriteString("\n```\n")
	return builder.String()
}
