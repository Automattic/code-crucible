package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
	cruciblerun "github.com/Automattic/code-crucible/internal/run"
	"github.com/Automattic/code-crucible/internal/scoring"
)

type tuiDashboardData struct {
	ProjectDir  string
	RunDir      string
	Config      model.RunConfig
	Leaderboard model.Leaderboard
	Results     []model.CandidateResult
}

type tuiDashboardModel struct {
	data         tuiDashboardData
	selected     int
	width        int
	height       int
	mode         tuiMode
	message      string
	form         tuiForm
	actionTitle  string
	actionOutput string
	actionError  string
	busy         bool
	actionStart  time.Time
	actionCmd    string
	actionCancel context.CancelFunc
	canceling    bool
	history      []tuiCommandHistoryEntry
	advanced     int
	spinner      spinner.Model
	table        table.Model
	detail       viewport.Model
	result       viewport.Model
	historyView  viewport.Model
}

type tuiMode int

const (
	tuiModeDashboard tuiMode = iota
	tuiModeForm
	tuiModeActionResult
	tuiModeHistory
	tuiModeAdvanced
)

type tuiAction string

const (
	tuiActionRun       tuiAction = "run"
	tuiActionDiscover  tuiAction = "discover"
	tuiActionGenerate  tuiAction = "generate"
	tuiActionEvaluator tuiAction = "evaluator"
	tuiActionEvaluate  tuiAction = "evaluate"
	tuiActionAdopt     tuiAction = "adopt"
	tuiActionPromote   tuiAction = "promote"
	tuiActionNextRound tuiAction = "next-round"
	tuiActionEvolve    tuiAction = "evolve"
	tuiActionReport    tuiAction = "report"
	tuiActionIndex     tuiAction = "index"
	tuiActionQuery     tuiAction = "query"
	tuiActionInspect   tuiAction = "inspect"
)

type tuiForm struct {
	Action  tuiAction
	Title   string
	Fields  []tuiFormField
	Focus   int
	Message string
}

type tuiFormField struct {
	Name        string
	Label       string
	Value       string
	Placeholder string
	Required    bool
	Input       textinput.Model
}

type tuiActionDoneMsg struct {
	Title       string
	Code        int
	Stdout      string
	Stderr      string
	Data        tuiDashboardData
	Err         error
	Canceled    bool
	CancelEvent *cruciblerun.CancellationEvent
	CancelErr   error
	History     int
	FinishedAt  time.Time
}

type tuiCommandHistoryEntry struct {
	Title             string
	Action            tuiAction
	Options           []tuiCommandHistoryOption
	Command           string
	StartedAt         time.Time
	FinishedAt        time.Time
	Code              int
	Error             string
	Canceled          bool
	StdoutBytes       int
	StderrBytes       int
	CancelEventPath   string
	UpdatedCandidates []string
	CancelError       string
}

type tuiCommandHistoryOption struct {
	Label string
	Value string
}

type tuiAdvancedAction struct {
	Label       string
	Description string
	Action      tuiAction
}

func runTUI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "latest", "run ID; defaults to latest run")
	noAltScreen := fs.Bool("no-alt-screen", false, "render inline instead of using the terminal alternate screen")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if stdin == nil {
		stdin = os.Stdin
	}

	data, message, autoStart, err := loadInitialTUIDashboard(*projectDir, *runID)
	if err != nil {
		fmt.Fprintf(stderr, "tui failed: %v\n", err)
		return 1
	}
	model := newTUIDashboardModel(data, message)
	if autoStart {
		model.openForm(tuiActionRun)
		model.form.Message = message
	}

	programOptions := []tea.ProgramOption{
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
	}
	if !*noAltScreen {
		programOptions = append(programOptions, tea.WithAltScreen())
	}
	program := tea.NewProgram(model, programOptions...)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(stderr, "tui failed: %v\n", err)
		return 1
	}
	return 0
}

func loadInitialTUIDashboard(projectDir, runSelector string) (tuiDashboardData, string, bool, error) {
	data, err := loadTUIDashboard(projectDir, runSelector)
	if err == nil {
		return data, "", false, nil
	}
	selector := strings.TrimSpace(runSelector)
	if selector != "" && selector != "latest" {
		return tuiDashboardData{}, "", false, err
	}
	absProject, absErr := filepath.Abs(defaultString(projectDir, "."))
	if absErr != nil {
		return tuiDashboardData{}, "", false, absErr
	}
	data = tuiDashboardData{
		ProjectDir: absProject,
		Config: model.RunConfig{
			ID:       "",
			Variants: defaultVariantCount,
			External: model.ExternalPolicy{Mode: model.ExternalModeDeny},
		},
	}
	if tuiProjectHasNoRuns(absProject) {
		return data, "No tournaments found yet. Enter an improvement request to start.", true, nil
	}
	return data, fmt.Sprintf("No tournament loaded yet: %v", err), false, nil
}

func tuiProjectHasNoRuns(projectDir string) bool {
	runsDir := filepath.Join(project.WorkDir(projectDir), "runs")
	entries, err := os.ReadDir(runsDir)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return false
		}
	}
	return true
}

func loadTUIDashboard(projectDir, runSelector string) (tuiDashboardData, error) {
	if strings.TrimSpace(projectDir) == "" {
		projectDir = "."
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return tuiDashboardData{}, err
	}
	configPath, err := archive.RunConfigPath(absProject, runSelector)
	if err != nil {
		return tuiDashboardData{}, err
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		return tuiDashboardData{}, err
	}
	runDir := cfg.RunDir
	if strings.TrimSpace(runDir) == "" {
		runDir = filepath.Dir(configPath)
	} else {
		runDir = archive.ProjectPath(absProject, runDir)
	}
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return tuiDashboardData{}, err
	}
	return tuiDashboardData{
		ProjectDir:  absProject,
		RunDir:      runDir,
		Config:      *cfg,
		Leaderboard: *board,
		Results:     rankedResults(board.Results),
	}, nil
}

func newTUIDashboardModel(data tuiDashboardData, message string) tuiDashboardModel {
	m := tuiDashboardModel{
		data:     data,
		selected: defaultCandidateSelectionIndex(data.Results),
		width:    100,
		message:  message,
		spinner:  spinner.New(),
	}
	m.configureBubbles()
	return m
}

func (m tuiDashboardModel) Init() tea.Cmd {
	return nil
}

func (m tuiDashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.configureBubbles()
	case tuiActionDoneMsg:
		m.busy = false
		m.actionStart = time.Time{}
		m.actionCancel = nil
		m.canceling = false
		m.mode = tuiModeDashboard
		m.actionTitle = msg.Title
		m.actionOutput = msg.Stdout
		m.actionError = msg.Stderr
		m.finishHistoryEntry(msg)
		if msg.Canceled {
			m.message = fmt.Sprintf("%s canceled", msg.Title)
			if msg.CancelEvent != nil {
				if len(msg.CancelEvent.UpdatedCandidates) > 0 {
					m.message += fmt.Sprintf("; marked canceled: %s", strings.Join(msg.CancelEvent.UpdatedCandidates, ", "))
				}
			}
		} else if msg.Err != nil {
			m.message = msg.Err.Error()
		} else if msg.Code != 0 {
			m.message = fmt.Sprintf("%s exited with status %d", msg.Title, msg.Code)
		} else {
			m.message = fmt.Sprintf("%s complete", msg.Title)
			if msg.Data.ProjectDir != "" {
				m.data = msg.Data
				m.selected = clampInt(m.selected, 0, len(m.data.Results)-1)
			}
		}
		m.message = tuiCommandHistoryHint(m.message)
		m.configureBubbles()
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case tea.KeyMsg:
		key := strings.ToLower(msg.String())
		if m.busy {
			switch key {
			case "ctrl+c", "esc", "c":
				if !m.canceling && m.actionCancel != nil {
					m.canceling = true
					m.message = "Cancel requested; waiting for action cleanup."
					m.actionCancel()
				}
			}
			return m, nil
		}
		switch m.mode {
		case tuiModeForm:
			return m.updateForm(msg)
		case tuiModeActionResult:
			switch key {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "b", "esc":
				m.mode = tuiModeDashboard
				return m, nil
			}
			var cmd tea.Cmd
			m.result, cmd = m.result.Update(msg)
			return m, cmd
		case tuiModeHistory:
			switch key {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "b", "esc", "h":
				m.mode = tuiModeDashboard
				return m, nil
			}
			var cmd tea.Cmd
			m.historyView, cmd = m.historyView.Update(msg)
			return m, cmd
		case tuiModeAdvanced:
			switch key {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "b", "esc", "?":
				m.mode = tuiModeDashboard
				return m, nil
			case "up", "k":
				m.advanced = clampInt(m.advanced-1, 0, len(m.advancedActions())-1)
				return m, nil
			case "down", "j":
				m.advanced = clampInt(m.advanced+1, 0, len(m.advancedActions())-1)
				return m, nil
			case "home":
				m.advanced = 0
				return m, nil
			case "end":
				m.advanced = maxInt(0, len(m.advancedActions())-1)
				return m, nil
			case "enter":
				actions := m.advancedActions()
				if len(actions) == 0 {
					m.mode = tuiModeDashboard
					return m, nil
				}
				m.advanced = clampInt(m.advanced, 0, len(actions)-1)
				m.openForm(actions[m.advanced].Action)
				return m, nil
			}
			return m, nil
		}
		switch key {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "?":
			m.advanced = clampInt(m.advanced, 0, len(m.advancedActions())-1)
			m.mode = tuiModeAdvanced
		case "s":
			m.openForm(tuiActionRun)
		case "h":
			m.mode = tuiModeHistory
		case "r":
			if m.selectedCandidateID() == "" {
				m.message = "No candidate is selected."
				return m, nil
			}
			m.form = m.newForm(tuiActionInspect)
			return m.submitForm()
		case "a":
			if m.selectedCandidateID() == "" {
				m.message = "No candidate is selected."
				return m, nil
			}
			if m.selectedCandidateIsBaseline() {
				m.message = "Apply is available after a non-baseline candidate is selected."
				return m, nil
			}
			m.openForm(tuiActionPromote)
		case "c":
			if !m.hasLoadedRun() {
				m.message = "Start a tournament first."
				return m, nil
			}
			m.openForm(tuiActionEvolve)
		case "t":
			if !m.hasLoadedRun() {
				m.message = "Start a tournament first."
				return m, nil
			}
			if m.needsEvaluator() {
				m.openForm(tuiActionEvaluator)
				m.form.Message = "Create a test harness before scoring candidates."
				break
			}
			m.openForm(tuiActionEvaluate)
		case "g":
			if !m.hasLoadedRun() {
				m.message = "Start a tournament first."
				return m, nil
			}
			m.openForm(tuiActionGenerate)
		case "e":
			if !m.hasLoadedRun() {
				m.message = "Start a tournament first."
				return m, nil
			}
			m.openForm(tuiActionReport)
		case "up", "down", "k", "j", "home", "end", "pgup", "pgdown":
			var cmd tea.Cmd
			m.table, cmd = m.table.Update(msg)
			m.selected = m.tableSelectedIndex()
			m.syncDetailViewport()
			return m, cmd
		default:
			return m, nil
		}
		m.configureBubbles()
	}
	return m, nil
}

func (m tuiDashboardModel) View() string {
	m.configureBubbles()
	switch m.mode {
	case tuiModeForm:
		return m.formView()
	case tuiModeActionResult:
		return m.actionResultView()
	case tuiModeHistory:
		return m.commandHistoryView()
	case tuiModeAdvanced:
		return m.advancedView()
	}
	if m.busy {
		return m.actionProgressView()
	}
	return m.dashboardView()
}

func (m tuiDashboardModel) dashboardView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Code Crucible\n")
	fmt.Fprintf(&b, "Project: %s\n", m.data.ProjectDir)
	if strings.TrimSpace(m.message) != "" {
		fmt.Fprintf(&b, "Status: %s\n", m.message)
	}
	if strings.TrimSpace(m.data.Config.ID) == "" {
		b.WriteString("\nNo tournament is loaded yet.\n\n")
	} else {
		fmt.Fprintf(&b, "Tournament: %s\n", m.data.Config.ID)
		fmt.Fprintf(&b, "Created: %s\n", m.data.Config.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
		fmt.Fprintf(&b, "Goal: %s\n", shorten(m.data.Config.Optimize, maxInt(24, width-10)))
		fmt.Fprintf(&b, "AI agent: %s  Round: %s  Candidates/request: %d  External requests: %s\n",
			displayValue(m.data.Config.Agent),
			filepath.Base(filepath.FromSlash(m.data.Config.RoundDir)),
			m.data.Config.Variants,
			displayValue(string(m.data.Config.External.Mode)),
		)
		passed, failed, canceled, pending := tuiStatusCounts(m.data.Results)
		fmt.Fprintf(&b, "Candidates: %d  Passed: %d  Failed: %d  Canceled: %d  Pending: %d\n\n", len(m.data.Results), passed, failed, canceled, pending)
	}

	if len(m.data.Results) == 0 {
		b.WriteString("No candidates have been created for this tournament yet.\n\n")
	} else {
		b.WriteString("Leaderboard\n")
		b.WriteString(m.table.View())
		b.WriteString("\n")
		b.WriteString("Candidate Detail\n")
		b.WriteString(m.detail.View())
		b.WriteString("\n")
	}

	b.WriteString("Next Actions\n")
	for _, action := range m.nextActions() {
		fmt.Fprintf(&b, "%s\n", action)
	}
	b.WriteString(m.dashboardKeyHelp())
	return b.String()
}

func (m *tuiDashboardModel) openForm(action tuiAction) {
	m.mode = tuiModeForm
	m.message = ""
	m.form = m.newForm(action)
	m.form.configureInputs(m.formWidth())
}

func (m tuiDashboardModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.form.configureInputs(m.formWidth())
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = tuiModeDashboard
		return m, nil
	case "tab", "down":
		m.form.Focus = nextFormFocus(m.form)
		m.form.configureInputs(m.formWidth())
		return m, nil
	case "shift+tab", "up":
		m.form.Focus = previousFormFocus(m.form)
		m.form.configureInputs(m.formWidth())
		return m, nil
	case "enter":
		return m.submitForm()
	}
	if len(m.form.Fields) > 0 {
		var cmd tea.Cmd
		field := &m.form.Fields[m.form.Focus]
		if msg.Type == tea.KeySpace {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
		}
		field.Input, cmd = field.Input.Update(msg)
		field.Value = field.Input.Value()
		return m, cmd
	}
	return m, nil
}

func (m tuiDashboardModel) submitForm() (tea.Model, tea.Cmd) {
	m.form.syncValues()
	if err := m.form.validate(); err != nil {
		m.form.Message = err.Error()
		return m, nil
	}
	title := m.form.Title
	form := m.form
	projectDir := m.data.ProjectDir
	startedAt := time.Now()
	command := form.commandPreview(projectDir, m.data.Config.ID)
	m.history = append(m.history, newTUICommandHistoryEntry(form, command, startedAt))
	historyIndex := len(m.history) - 1
	m.mode = tuiModeDashboard
	m.busy = true
	m.actionTitle = title
	m.actionOutput = ""
	m.actionError = ""
	m.actionStart = startedAt
	m.actionCmd = command
	ctx, cancel := context.WithCancel(context.Background())
	m.actionCancel = cancel
	m.canceling = false
	m.message = ""
	runCmd := func() tea.Msg {
		var stdout, stderr strings.Builder
		controller := WorkflowController{
			Context:    ctx,
			ProjectDir: projectDir,
			Stdout:     &stdout,
			Stderr:     &stderr,
		}
		code := runTUIFormAction(controller, form)
		canceled := ctx.Err() == context.Canceled
		var cancelEvent *cruciblerun.CancellationEvent
		var cancelErr error
		if canceled {
			cancelEvent, cancelErr = markTUIActionCanceled(projectDir, form)
			if cancelEvent != nil && len(cancelEvent.UpdatedCandidates) > 0 {
				fmt.Fprintf(&stdout, "Marked canceled candidates: %s\n", strings.Join(cancelEvent.UpdatedCandidates, ", "))
			}
		}
		data, err := loadTUIDashboard(projectDir, "latest")
		if err != nil {
			data = m.data
			if code != 0 || form.Action != tuiActionRun {
				err = nil
			}
		}
		return tuiActionDoneMsg{
			Title:       title,
			Code:        code,
			Stdout:      stdout.String(),
			Stderr:      stderr.String(),
			Data:        data,
			Err:         err,
			Canceled:    canceled,
			CancelEvent: cancelEvent,
			CancelErr:   cancelErr,
			History:     historyIndex,
			FinishedAt:  time.Now(),
		}
	}
	return m, tea.Batch(runCmd, tuiSpinnerTick(m.spinner))
}

func (m tuiDashboardModel) formView() string {
	m.form.configureInputs(m.formWidth())
	var b strings.Builder
	fmt.Fprintf(&b, "Code Crucible\n\n%s\n", m.form.Title)
	if strings.TrimSpace(m.form.Message) != "" {
		fmt.Fprintf(&b, "Status: %s\n", m.form.Message)
	}
	for i, field := range m.form.Fields {
		marker := " "
		if i == m.form.Focus {
			marker = ">"
		}
		required := ""
		if field.Required {
			required = " *"
		}
		fmt.Fprintf(&b, "%s %s%s: %s\n", marker, field.Label, required, field.Input.View())
	}
	fmt.Fprintf(&b, "\nCLI Equivalent\n%s\n", m.form.commandPreview(m.data.ProjectDir, m.data.Config.ID))
	b.WriteString("\nKeys: type/edit, tab/down next field, up previous field, ctrl+u clear, enter submit, esc cancel, ctrl+c quit\n")
	return b.String()
}

func (m tuiDashboardModel) actionResultView() string {
	m.syncResultViewport()
	var b strings.Builder
	fmt.Fprintf(&b, "Code Crucible\n\n")
	if m.busy {
		return m.actionProgressView()
	}
	fmt.Fprintf(&b, "%s\n", displayValue(m.message))
	if strings.TrimSpace(m.result.View()) != "" {
		fmt.Fprintf(&b, "\n%s\n", m.result.View())
	}
	b.WriteString("\nKeys: j/k scroll, pgup/pgdown page, b back to dashboard, q quit\n")
	return b.String()
}

func (m tuiDashboardModel) commandHistoryView() string {
	m.syncHistoryViewport()
	var b strings.Builder
	fmt.Fprintf(&b, "Code Crucible\n\nCommand History\n")
	if strings.TrimSpace(m.historyView.View()) != "" {
		fmt.Fprintf(&b, "\n%s\n", m.historyView.View())
	}
	b.WriteString("\nKeys: j/k scroll, pgup/pgdown page, b back to dashboard, q quit\n")
	return b.String()
}

func (m tuiDashboardModel) advancedView() string {
	actions := m.advancedActions()
	selected := clampInt(m.advanced, 0, len(actions)-1)
	var b strings.Builder
	fmt.Fprintf(&b, "Code Crucible\n\nAdvanced\n")
	if len(actions) == 0 {
		b.WriteString("\nNo advanced actions are available yet.\n")
	} else {
		for i, action := range actions {
			marker := " "
			if i == selected {
				marker = ">"
			}
			fmt.Fprintf(&b, "%s %s\n", marker, action.Label)
			if strings.TrimSpace(action.Description) != "" {
				fmt.Fprintf(&b, "  %s\n", action.Description)
			}
		}
	}
	b.WriteString("\nKeys: j/k select, enter open, b back to dashboard, q quit\n")
	return b.String()
}

func (m tuiDashboardModel) actionProgressView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Code Crucible\n\n")
	fmt.Fprintf(&b, "%s Running %s\n", m.spinner.View(), displayValue(m.actionTitle))
	if !m.actionStart.IsZero() {
		fmt.Fprintf(&b, "Elapsed: %s\n", formatTUIDuration(time.Since(m.actionStart)))
	}
	if len(m.history) > 0 {
		entry := m.history[len(m.history)-1]
		if len(entry.Options) > 0 {
			b.WriteString("\nSelected Options\n")
			for _, option := range entry.Options {
				fmt.Fprintf(&b, "%s: %s\n", option.Label, displayValue(option.Value))
			}
		}
		if steps := tuiActionProgressSteps(entry); len(steps) > 0 {
			b.WriteString("\nExpected Steps\n")
			for i, step := range steps {
				fmt.Fprintf(&b, "%d. %s\n", i+1, step)
			}
		}
	}
	b.WriteString("\nThe dashboard will refresh when this finishes. Press h there for command history and captured output summaries.\n")
	if m.canceling {
		b.WriteString("Cancel requested; waiting for action cleanup.\n")
	} else {
		b.WriteString("Keys: c or esc request cancellation\n")
	}
	return b.String()
}

func (m tuiDashboardModel) newForm(action tuiAction) tuiForm {
	runID := defaultString(m.data.Config.ID, "latest")
	agent := m.data.Config.Agent
	candidateID := m.selectedCandidateID()
	variants := m.data.Config.Variants
	if variants <= 0 {
		variants = defaultVariantCount
	}
	switch action {
	case tuiActionRun:
		return tuiForm{
			Action: action,
			Title:  "Start Tournament",
			Fields: []tuiFormField{
				{Name: "optimize", Label: "What do you want to improve?", Placeholder: "reduce p95 latency in checkout pricing", Required: true},
				{Name: "auto", Label: "Find code and tests automatically", Value: "true"},
				{Name: "generate", Label: "Create candidate code", Value: "true"},
				{Name: "evaluate", Label: "Test and score candidates", Value: "true"},
				{Name: "source_path", Label: "Code to optimize", Placeholder: "auto-detect when blank"},
				{Name: "agent", Label: "AI agent", Value: agent, Placeholder: "project default"},
				{Name: "variants", Label: "Candidates to create", Value: strconv.Itoa(variants)},
			},
		}
	case tuiActionDiscover:
		return tuiForm{
			Action: action,
			Title:  "Discovery Only",
			Fields: []tuiFormField{
				{Name: "request", Label: "What do you want to improve?", Placeholder: "find the code behind checkout pricing", Required: true},
				{Name: "agent", Label: "Discovery agent", Value: "local"},
			},
		}
	case tuiActionGenerate:
		return tuiForm{
			Action: action,
			Title:  "Create Candidates",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "agent", Label: "AI agent", Value: agent, Placeholder: "run or project default"},
			},
		}
	case tuiActionEvaluator:
		return tuiForm{
			Action: action,
			Title:  "Create Test Harness",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "agent", Label: "AI agent", Value: agent, Placeholder: "run or project default"},
				{Name: "validation_timeout", Label: "Validation timeout", Value: "60s"},
			},
		}
	case tuiActionEvaluate:
		return tuiForm{
			Action: action,
			Title:  "Test & Score Candidates",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "candidate", Label: "Only test this candidate", Placeholder: "all candidates"},
				{Name: "jobs", Label: "Parallel tests", Value: "1"},
				{Name: "external_routing", Label: "Container network routing", Placeholder: "default routing"},
			},
		}
	case tuiActionAdopt:
		return tuiForm{
			Action: action,
			Title:  "Import Generated Candidates",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "model", Label: "Model", Placeholder: "provider default"},
				{Name: "json", Label: "Machine-readable output", Value: "false"},
			},
		}
	case tuiActionPromote:
		return tuiForm{
			Action: action,
			Title:  "Apply Selected Candidate",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "candidate", Label: "Candidate", Value: candidateID},
				{Name: "dry_run", Label: "Preview only", Value: "false"},
				{Name: "allow_unpassed", Label: "Allow failed candidate", Value: "false"},
				{Name: "json", Label: "Machine-readable output", Value: "false"},
			},
		}
	case tuiActionNextRound:
		return tuiForm{
			Action: action,
			Title:  "Create Next Round",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "parents", Label: "Candidates to carry forward", Value: strconv.Itoa(variants)},
				{Name: "json", Label: "Machine-readable output", Value: "false"},
			},
		}
	case tuiActionEvolve:
		return tuiForm{
			Action: action,
			Title:  "Continue Tournament",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "rounds", Label: "Rounds to run", Value: "1"},
				{Name: "parents", Label: "Candidates to carry forward", Value: strconv.Itoa(variants)},
				{Name: "agent", Label: "AI agent", Value: agent},
			},
		}
	case tuiActionReport:
		return tuiForm{
			Action: action,
			Title:  "Export Report",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "output", Label: "Output path", Placeholder: "default report path"},
				{Name: "json", Label: "Machine-readable output", Value: "false"},
			},
		}
	case tuiActionIndex:
		return tuiForm{
			Action: action,
			Title:  "Refresh Archive Index",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament (blank for all)", Value: runID},
				{Name: "json", Label: "Machine-readable output", Value: "false"},
			},
		}
	case tuiActionQuery:
		return tuiForm{
			Action: action,
			Title:  "Browse Archive",
			Fields: []tuiFormField{
				{Name: "kind", Label: "Kind", Value: "runs", Required: true},
				{Name: "limit", Label: "Limit", Value: "10"},
				{Name: "run", Label: "Tournament for candidates", Value: runID},
				{Name: "status", Label: "Candidate status", Placeholder: "any status"},
			},
		}
	case tuiActionInspect:
		return tuiForm{
			Action: action,
			Title:  "Review Selected Candidate",
			Fields: []tuiFormField{
				{Name: "run", Label: "Tournament", Value: runID, Required: true},
				{Name: "candidate", Label: "Candidate", Value: candidateID},
			},
		}
	default:
		return tuiForm{Action: action, Title: string(action)}
	}
}

func runTUIFormAction(controller WorkflowController, form tuiForm) int {
	switch form.Action {
	case tuiActionRun:
		variants := parsePositiveInt(form.value("variants"), defaultVariantCount)
		return controller.Run(RunWorkflowOptions{
			Optimize:   form.value("optimize"),
			Auto:       parseTUIBool(form.value("auto")),
			SourcePath: form.value("source_path"),
			Agent:      form.value("agent"),
			Variants:   variants,
			Generate:   parseTUIBool(form.value("generate")),
			Evaluate:   parseTUIBool(form.value("evaluate")),
		})
	case tuiActionDiscover:
		return controller.Discover(DiscoverWorkflowOptions{
			Agent:   defaultString(form.value("agent"), "local"),
			Request: form.value("request"),
		})
	case tuiActionGenerate:
		return controller.Generate(GenerateWorkflowOptions{
			RunSelector: form.value("run"),
			Agent:       form.value("agent"),
		})
	case tuiActionEvaluator:
		extraArgs := []string{}
		if timeout := strings.TrimSpace(form.value("validation_timeout")); timeout != "" {
			extraArgs = append(extraArgs, "--validation-timeout", timeout)
		}
		return controller.EvaluatorGenerate(EvaluatorGenerateWorkflowOptions{
			RunSelector: form.value("run"),
			Agent:       form.value("agent"),
			ExtraArgs:   extraArgs,
		})
	case tuiActionEvaluate:
		extraArgs := []string{}
		if candidate := strings.TrimSpace(form.value("candidate")); candidate != "" {
			extraArgs = append(extraArgs, "--candidate", candidate)
		}
		if jobs := parsePositiveInt(form.value("jobs"), 0); jobs > 0 {
			extraArgs = append(extraArgs, "--jobs", strconv.Itoa(jobs))
		}
		if routing := strings.TrimSpace(form.value("external_routing")); routing != "" {
			extraArgs = append(extraArgs, "--external-routing", routing)
		}
		return controller.Evaluate(EvaluateWorkflowOptions{
			RunSelector: form.value("run"),
			ExtraArgs:   extraArgs,
		})
	case tuiActionAdopt:
		return controller.Adopt(AdoptWorkflowOptions{
			RunSelector: form.value("run"),
			Model:       form.value("model"),
			JSON:        parseTUIBool(form.value("json")),
		})
	case tuiActionPromote:
		return controller.Promote(PromoteWorkflowOptions{
			RunSelector:   form.value("run"),
			CandidateID:   form.value("candidate"),
			DryRun:        parseTUIBool(form.value("dry_run")),
			AllowUnpassed: parseTUIBool(form.value("allow_unpassed")),
			JSON:          parseTUIBool(form.value("json")),
		})
	case tuiActionNextRound:
		return controller.NextRound(NextRoundWorkflowOptions{
			RunSelector: form.value("run"),
			Parents:     parsePositiveInt(form.value("parents"), defaultVariantCount),
			JSON:        parseTUIBool(form.value("json")),
		})
	case tuiActionEvolve:
		return controller.Evolve(EvolveWorkflowOptions{
			RunSelector: form.value("run"),
			Rounds:      parsePositiveInt(form.value("rounds"), 1),
			Parents:     parsePositiveInt(form.value("parents"), defaultVariantCount),
			Agent:       form.value("agent"),
		})
	case tuiActionReport:
		return controller.Report(ReportWorkflowOptions{
			RunSelector: form.value("run"),
			OutputPath:  form.value("output"),
			JSON:        parseTUIBool(form.value("json")),
		})
	case tuiActionIndex:
		runSelector := form.value("run")
		return controller.Index(IndexWorkflowOptions{
			RunSelector: runSelector,
			ScopedRun:   strings.TrimSpace(runSelector) != "",
			JSON:        parseTUIBool(form.value("json")),
		})
	case tuiActionQuery:
		return controller.Query(QueryWorkflowOptions{
			Kind:        defaultString(form.value("kind"), "runs"),
			Limit:       parsePositiveInt(form.value("limit"), 10),
			RunSelector: form.value("run"),
			Status:      form.value("status"),
		})
	case tuiActionInspect:
		return controller.Inspect(InspectWorkflowOptions{
			RunSelector: form.value("run"),
			CandidateID: form.value("candidate"),
		})
	default:
		fmt.Fprintf(controller.Stderr, "unsupported TUI action %q\n", form.Action)
		return 2
	}
}

func markTUIActionCanceled(projectDir string, form tuiForm) (*cruciblerun.CancellationEvent, error) {
	runSelector := strings.TrimSpace(form.value("run"))
	candidateID := strings.TrimSpace(form.value("candidate"))
	switch form.Action {
	case tuiActionGenerate, tuiActionEvaluator, tuiActionEvaluate, tuiActionEvolve, tuiActionReport:
		if runSelector == "" {
			runSelector = "latest"
		}
	case tuiActionQuery:
		if strings.TrimSpace(form.value("kind")) != "candidates" {
			return nil, nil
		}
		if runSelector == "" {
			runSelector = "latest"
		}
	default:
		return nil, nil
	}
	return cruciblerun.MarkCanceled(cruciblerun.CancellationOptions{
		ProjectDir:  projectDir,
		RunID:       runSelector,
		Action:      string(form.Action),
		CandidateID: candidateID,
		Reason:      fmt.Sprintf("%s canceled by user from TUI.", form.Title),
	})
}

func (f tuiForm) validate() error {
	for _, field := range f.Fields {
		if field.Required && strings.TrimSpace(field.Value) == "" {
			return fmt.Errorf("%s is required", field.Label)
		}
	}
	switch f.Action {
	case tuiActionRun:
		if value := strings.TrimSpace(f.value("variants")); value != "" {
			if _, err := strconv.Atoi(value); err != nil {
				return fmt.Errorf("Candidates to create must be a number")
			}
		}
	case tuiActionEvaluate:
		if value := strings.TrimSpace(f.value("jobs")); value != "" {
			if _, err := strconv.Atoi(value); err != nil {
				return fmt.Errorf("Parallel tests must be a number")
			}
		}
	case tuiActionNextRound:
		if value := strings.TrimSpace(f.value("parents")); value != "" {
			if parsed, err := strconv.Atoi(value); err != nil || parsed < 1 {
				return fmt.Errorf("Candidates to carry forward must be a positive number")
			}
		}
	case tuiActionEvolve:
		if value := strings.TrimSpace(f.value("rounds")); value != "" {
			if parsed, err := strconv.Atoi(value); err != nil || parsed < 1 {
				return fmt.Errorf("Rounds to run must be a positive number")
			}
		}
		if value := strings.TrimSpace(f.value("parents")); value != "" {
			if parsed, err := strconv.Atoi(value); err != nil || parsed < 1 {
				return fmt.Errorf("Candidates to carry forward must be a positive number")
			}
		}
	case tuiActionEvaluator:
		if value := strings.TrimSpace(f.value("validation_timeout")); value != "" {
			if _, err := time.ParseDuration(value); err != nil {
				return fmt.Errorf("Validation timeout must be a duration such as 60s")
			}
		}
	case tuiActionQuery:
		if kind := strings.TrimSpace(f.value("kind")); kind != "" && kind != "runs" && kind != "candidates" {
			return fmt.Errorf("Kind must be runs or candidates")
		}
		if value := strings.TrimSpace(f.value("limit")); value != "" {
			if _, err := strconv.Atoi(value); err != nil {
				return fmt.Errorf("Limit must be a number")
			}
		}
	}
	return nil
}

func (f tuiForm) commandPreview(projectDir, runID string) string {
	project := shellQuote(projectDir)
	switch f.Action {
	case tuiActionRun:
		args := []string{"crucible", "run", "--project-dir", project}
		if sourcePath := strings.TrimSpace(f.value("source_path")); sourcePath != "" {
			args = append(args, "--source-path", shellQuote(sourcePath))
		}
		if parseTUIBool(f.value("auto")) {
			args = append(args, "--auto")
		}
		if agent := strings.TrimSpace(f.value("agent")); agent != "" {
			args = append(args, "--agent", shellQuote(agent))
		}
		if variants := strings.TrimSpace(f.value("variants")); variants != "" {
			args = append(args, "--variants", shellQuote(variants))
		}
		if parseTUIBool(f.value("generate")) {
			args = append(args, "--generate")
		}
		if parseTUIBool(f.value("evaluate")) {
			args = append(args, "--evaluate")
		}
		task := f.value("optimize")
		if strings.TrimSpace(task) == "" {
			task = "<improvement request>"
		}
		args = append(args, shellQuote(task))
		return strings.Join(args, " ")
	case tuiActionDiscover:
		request := f.value("request")
		if strings.TrimSpace(request) == "" {
			request = "<improvement request>"
		}
		return fmt.Sprintf("crucible discover --project-dir %s --agent %s %s", project, shellQuote(defaultString(f.value("agent"), "local")), shellQuote(request))
	case tuiActionGenerate:
		args := []string{"crucible", "generate", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if agent := strings.TrimSpace(f.value("agent")); agent != "" {
			args = append(args, "--agent", shellQuote(agent))
		}
		return strings.Join(args, " ")
	case tuiActionEvaluator:
		args := []string{"crucible", "evaluator", "generate", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if agent := strings.TrimSpace(f.value("agent")); agent != "" {
			args = append(args, "--agent", shellQuote(agent))
		}
		if timeout := strings.TrimSpace(f.value("validation_timeout")); timeout != "" {
			args = append(args, "--validation-timeout", shellQuote(timeout))
		}
		return strings.Join(args, " ")
	case tuiActionEvaluate:
		args := []string{"crucible", "evaluate", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if candidate := strings.TrimSpace(f.value("candidate")); candidate != "" {
			args = append(args, "--candidate", shellQuote(candidate))
		}
		if jobs := strings.TrimSpace(f.value("jobs")); jobs != "" {
			args = append(args, "--jobs", shellQuote(jobs))
		}
		if routing := strings.TrimSpace(f.value("external_routing")); routing != "" {
			args = append(args, "--external-routing", shellQuote(routing))
		}
		return strings.Join(args, " ")
	case tuiActionAdopt:
		args := []string{"crucible", "adopt", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if model := strings.TrimSpace(f.value("model")); model != "" {
			args = append(args, "--model", shellQuote(model))
		}
		if parseTUIBool(f.value("json")) {
			args = append(args, "--json")
		}
		return strings.Join(args, " ")
	case tuiActionPromote:
		args := []string{"crucible", "promote", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if candidate := strings.TrimSpace(f.value("candidate")); candidate != "" {
			args = append(args, "--candidate", shellQuote(candidate))
		}
		if parseTUIBool(f.value("dry_run")) {
			args = append(args, "--dry-run")
		}
		if parseTUIBool(f.value("allow_unpassed")) {
			args = append(args, "--allow-unpassed")
		}
		if parseTUIBool(f.value("json")) {
			args = append(args, "--json")
		}
		return strings.Join(args, " ")
	case tuiActionNextRound:
		args := []string{"crucible", "next-round", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if parents := strings.TrimSpace(f.value("parents")); parents != "" {
			args = append(args, "--parents", shellQuote(parents))
		}
		if parseTUIBool(f.value("json")) {
			args = append(args, "--json")
		}
		return strings.Join(args, " ")
	case tuiActionEvolve:
		args := []string{"crucible", "evolve", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if rounds := strings.TrimSpace(f.value("rounds")); rounds != "" {
			args = append(args, "--rounds", shellQuote(rounds))
		}
		if parents := strings.TrimSpace(f.value("parents")); parents != "" {
			args = append(args, "--parents", shellQuote(parents))
		}
		if agent := strings.TrimSpace(f.value("agent")); agent != "" {
			args = append(args, "--agent", shellQuote(agent))
		}
		return strings.Join(args, " ")
	case tuiActionReport:
		args := []string{"crucible", "report", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if output := strings.TrimSpace(f.value("output")); output != "" {
			args = append(args, "--output", shellQuote(output))
		}
		if parseTUIBool(f.value("json")) {
			args = append(args, "--json")
		}
		return strings.Join(args, " ")
	case tuiActionIndex:
		args := []string{"crucible", "index", "--project-dir", project}
		if run := strings.TrimSpace(f.value("run")); run != "" {
			args = append(args, "--run", shellQuote(run))
		}
		if parseTUIBool(f.value("json")) {
			args = append(args, "--json")
		}
		return strings.Join(args, " ")
	case tuiActionQuery:
		args := []string{"crucible", "query", shellQuote(defaultString(f.value("kind"), "runs")), "--project-dir", project}
		if limit := strings.TrimSpace(f.value("limit")); limit != "" {
			args = append(args, "--limit", shellQuote(limit))
		}
		if strings.TrimSpace(f.value("kind")) == "candidates" {
			if run := strings.TrimSpace(f.value("run")); run != "" {
				args = append(args, "--run", shellQuote(run))
			}
			if status := strings.TrimSpace(f.value("status")); status != "" {
				args = append(args, "--status", shellQuote(status))
			}
		}
		return strings.Join(args, " ")
	case tuiActionInspect:
		args := []string{"crucible", "inspect", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if candidate := strings.TrimSpace(f.value("candidate")); candidate != "" {
			args = append(args, shellQuote(candidate))
		}
		return strings.Join(args, " ")
	default:
		return ""
	}
}

func (f tuiForm) value(name string) string {
	for _, field := range f.Fields {
		if field.Name == name {
			if field.Input.Width > 0 {
				return strings.TrimSpace(field.Input.Value())
			}
			return strings.TrimSpace(field.Value)
		}
	}
	return ""
}

func (f *tuiForm) configureInputs(width int) {
	if f == nil {
		return
	}
	if width <= 0 {
		width = 64
	}
	for i := range f.Fields {
		field := &f.Fields[i]
		if field.Input.Width == 0 {
			input := textinput.New()
			input.Prompt = ""
			input.Placeholder = field.Placeholder
			input.SetValue(field.Value)
			input.SetCursor(len([]rune(field.Value)))
			field.Input = input
		}
		field.Input.Width = width
		if i == f.Focus {
			field.Input.Focus()
		} else {
			field.Input.Blur()
		}
		field.Value = field.Input.Value()
	}
}

func (f *tuiForm) syncValues() {
	if f == nil {
		return
	}
	for i := range f.Fields {
		if f.Fields[i].Input.Width > 0 {
			f.Fields[i].Value = f.Fields[i].Input.Value()
		}
	}
}

func nextFormFocus(form tuiForm) int {
	if len(form.Fields) == 0 {
		return 0
	}
	return (form.Focus + 1) % len(form.Fields)
}

func previousFormFocus(form tuiForm) int {
	if len(form.Fields) == 0 {
		return 0
	}
	if form.Focus <= 0 {
		return len(form.Fields) - 1
	}
	return form.Focus - 1
}

func (m tuiDashboardModel) formWidth() int {
	width := m.width - 28
	if width < 24 {
		width = 24
	}
	if width > 96 {
		width = 96
	}
	return width
}

func tuiSpinnerTick(sp spinner.Model) tea.Cmd {
	return func() tea.Msg {
		return sp.Tick()
	}
}

func formatTUIDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	if d < time.Hour {
		minutes := int(d / time.Minute)
		seconds := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	hours := int(d / time.Hour)
	minutes := int((d % time.Hour) / time.Minute)
	return fmt.Sprintf("%dh%02dm", hours, minutes)
}

func formatTUITimestamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

func tuiCommandHistoryHint(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "Press h for command history."
	}
	if strings.Contains(message, "command history") {
		return message
	}
	return message + " Press h for command history."
}

func (m *tuiDashboardModel) configureBubbles() {
	if m == nil {
		return
	}
	m.configureCandidateTable()
	m.syncDetailViewport()
	m.syncResultViewport()
	m.syncHistoryViewport()
}

func (m *tuiDashboardModel) configureCandidateTable() {
	if m == nil {
		return
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	columns := tuiCandidateTableColumns(width, leaderboardHasExternalCalls(m.data.Results))
	rows := m.candidateTableRows()
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(tuiCandidateTableHeight(m.height, len(rows))),
	)
	t.SetWidth(width)
	t.Focus()
	if m.selected > 0 {
		t.MoveDown(m.selected)
	}
	m.table = t
}

func (m tuiDashboardModel) candidateTableRows() []table.Row {
	scoreValues := make([]float64, 0, len(m.data.Results))
	p95Values := make([]float64, 0, len(m.data.Results))
	nsPerOpValues := make([]float64, 0, len(m.data.Results))
	for _, result := range m.data.Results {
		scoreValues = append(scoreValues, result.Score)
		p95Values = append(p95Values, result.Metrics.P95LatencyMS)
		nsPerOpValues = append(nsPerOpValues, result.Metrics.BenchmarkNsPerOp)
	}
	scoreFormatter := newDynamicFloatFormatter(scoreValues, 2, 4)
	p95Formatter := newDynamicFloatFormatter(p95Values, 2, 6)
	nsPerOpFormatter := newDynamicFloatFormatter(nsPerOpValues, 0, 4)
	baselinePrimary := leaderboardBaselinePrimary(m.data.Results)
	baselineMemory := leaderboardBaselineMemory(m.data.Results)
	showExternalCalls := leaderboardHasExternalCalls(m.data.Results)

	rows := make([]table.Row, 0, len(m.data.Results))
	for i, result := range m.data.Results {
		rank := "-"
		if result.Status == "passed" {
			rank = strconv.Itoa(i + 1)
		}
		row := table.Row{
			rank,
			result.Candidate.ID,
			result.Status,
			scoreFormatter.format(result.Score),
			formatOptionalFloat(p95Formatter, result.Metrics.P95LatencyMS),
			formatOptionalFloat(nsPerOpFormatter, result.Metrics.BenchmarkNsPerOp),
			formatSpeedup(baselinePrimary, scoring.PrimaryMetric(result.Metrics)),
			formatBytes(result.Metrics.MemoryPeakBytes),
			formatRelativeUsage(scoring.MemoryMetric(result.Metrics), baselineMemory),
		}
		if showExternalCalls {
			row = append(row, strconv.Itoa(leaderboardExternalCallCount(result)))
		}
		rows = append(rows, row)
	}
	return rows
}

func tuiCandidateTableColumns(width int, showExternalCalls bool) []table.Column {
	if width <= 0 {
		width = 100
	}
	candidateWidth := 28
	if width < 90 {
		candidateWidth = 20
	}
	if width < 72 {
		candidateWidth = 16
	}
	columns := []table.Column{
		{Title: "Rank", Width: 4},
		{Title: "Candidate", Width: candidateWidth},
		{Title: "Status", Width: 8},
		{Title: "Score", Width: 7},
		{Title: "P95 ms", Width: 10},
		{Title: "ns/op", Width: 10},
		{Title: "Speed", Width: 7},
		{Title: "Memory", Width: 9},
		{Title: "Mem/Base", Width: 8},
	}
	if showExternalCalls {
		columns = append(columns, table.Column{Title: "Ext", Width: 5})
	}
	return columns
}

func tuiCandidateTableHeight(windowHeight, rows int) int {
	height := 8
	if windowHeight > 0 {
		height = windowHeight / 3
	}
	if height < 5 {
		height = 5
	}
	if rows > 0 && height > rows+1 {
		height = rows + 1
	}
	return height
}

func (m tuiDashboardModel) tableSelectedIndex() int {
	selected := m.table.SelectedRow()
	if len(selected) < 2 {
		return clampInt(m.selected, 0, len(m.data.Results)-1)
	}
	candidateID := selected[1]
	for i, result := range m.data.Results {
		if result.Candidate.ID == candidateID {
			return i
		}
	}
	return clampInt(m.selected, 0, len(m.data.Results)-1)
}

func (m *tuiDashboardModel) syncDetailViewport() {
	if m == nil {
		return
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := 8
	if m.height > 0 {
		height = m.height / 4
	}
	if height < 6 {
		height = 6
	}
	m.detail.Width = width
	m.detail.Height = height
	m.detail.SetContent(strings.TrimPrefix(m.candidateDetail(), "Candidate Detail\n"))
}

func (m *tuiDashboardModel) syncResultViewport() {
	if m == nil {
		return
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height - 6
	if height < 10 {
		height = 18
	}
	var b strings.Builder
	if strings.TrimSpace(m.actionOutput) != "" {
		fmt.Fprintf(&b, "Output\n%s\n", strings.TrimRight(m.actionOutput, "\n"))
	}
	if strings.TrimSpace(m.actionError) != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Errors\n%s\n", strings.TrimRight(m.actionError, "\n"))
	}
	m.result.Width = width
	m.result.Height = height
	m.result.SetContent(b.String())
}

func (m *tuiDashboardModel) syncHistoryViewport() {
	if m == nil {
		return
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height - 6
	if height < 10 {
		height = 18
	}
	m.historyView.Width = width
	m.historyView.Height = height
	m.historyView.SetContent(m.commandHistoryContent())
}

func (m tuiDashboardModel) commandHistoryContent() string {
	if len(m.history) == 0 {
		return "No TUI commands have run in this session."
	}
	var b strings.Builder
	for i := len(m.history) - 1; i >= 0; i-- {
		entry := m.history[i]
		fmt.Fprintf(&b, "%d. %s  %s\n", len(m.history)-i, entry.Title, entry.status())
		fmt.Fprintf(&b, "Started: %s", formatTUITimestamp(entry.StartedAt))
		if !entry.FinishedAt.IsZero() {
			fmt.Fprintf(&b, "  Duration: %s", formatTUIDuration(entry.FinishedAt.Sub(entry.StartedAt)))
		}
		b.WriteString("\n")
		if len(entry.Options) > 0 {
			b.WriteString("Options\n")
			for _, option := range entry.Options {
				fmt.Fprintf(&b, "  %s: %s\n", option.Label, displayValue(option.Value))
			}
		}
		if strings.TrimSpace(entry.Command) != "" {
			fmt.Fprintf(&b, "Command\n  %s\n", entry.Command)
		}
		if entry.StdoutBytes > 0 || entry.StderrBytes > 0 {
			fmt.Fprintf(&b, "Captured output: stdout %d B, stderr %d B\n", entry.StdoutBytes, entry.StderrBytes)
		}
		if entry.CancelEventPath != "" {
			fmt.Fprintf(&b, "Cancellation event: %s\n", entry.CancelEventPath)
		}
		if len(entry.UpdatedCandidates) > 0 {
			fmt.Fprintf(&b, "Marked canceled: %s\n", strings.Join(entry.UpdatedCandidates, ", "))
		}
		if entry.CancelError != "" {
			fmt.Fprintf(&b, "Cancellation artifact update failed: %s\n", entry.CancelError)
		}
		if i > 0 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func tuiActionProgressSteps(entry tuiCommandHistoryEntry) []string {
	switch entry.Action {
	case tuiActionRun:
		steps := []string{}
		if tuiHistoryOptionBool(entry.Options, "Find code and tests automatically") {
			steps = append(steps, "Find source code, interfaces, and test setup")
		} else {
			steps = append(steps, "Use the supplied code path and run settings")
		}
		steps = append(steps, "Create the tournament archive and baseline candidate")
		if tuiHistoryOptionBool(entry.Options, "Create candidate code") {
			steps = append(steps, "Create candidate code with the selected AI agent")
		}
		if tuiHistoryOptionBool(entry.Options, "Test and score candidates") {
			steps = append(steps, "Test and score the baseline and candidates")
		}
		steps = append(steps, "Refresh the leaderboard")
		return steps
	case tuiActionDiscover:
		return []string{
			"Search for likely source files and interfaces",
			"Write a discovery plan into the work area",
			"Refresh the dashboard",
		}
	case tuiActionGenerate:
		return []string{
			"Ask the AI agent to create candidate code",
			"Import generated candidates into the tournament archive",
			"Refresh the leaderboard",
		}
	case tuiActionEvaluator:
		return []string{
			"Ask the AI agent to draft the test harness",
			"Validate the harness against the baseline candidate",
			"Record baseline metrics and refresh the leaderboard",
		}
	case tuiActionEvaluate:
		return []string{
			"Run the test harness for selected candidates",
			"Record correctness, timing, memory, CPU, and external-call results",
			"Update scores and refresh the leaderboard",
		}
	case tuiActionAdopt:
		return []string{
			"Scan generated candidate directories",
			"Validate candidate metadata and required files",
			"Add valid candidates to the leaderboard",
		}
	case tuiActionPromote:
		applyStep := "Apply the candidate code to the project"
		if tuiHistoryOptionBool(entry.Options, "Preview only") {
			applyStep = "Preview the project files that would change"
		}
		return []string{
			"Validate the selected candidate and target source path",
			applyStep,
			"Write a promotion report into the run archive",
		}
	case tuiActionNextRound:
		return []string{
			"Select leading candidates to carry forward",
			"Create the next-round work area and prompt",
			"Refresh the dashboard",
		}
	case tuiActionEvolve:
		return []string{
			"Carry forward the strongest candidates",
			"Create and import new candidate code",
			"Test, score, and refresh the leaderboard",
		}
	case tuiActionReport:
		return []string{
			"Render the tournament report",
			"Write the report artifact",
			"Refresh command history",
		}
	case tuiActionIndex:
		return []string{
			"Scan tournament archives",
			"Rebuild the archive index",
			"Refresh command history",
		}
	case tuiActionQuery:
		return []string{
			"Read the archive index",
			"Filter matching records",
			"Show the results",
		}
	case tuiActionInspect:
		return []string{
			"Read selected candidate artifacts",
			"Summarize metrics, verdicts, and source paths",
			"Refresh command history",
		}
	default:
		return nil
	}
}

func tuiHistoryOptionBool(options []tuiCommandHistoryOption, label string) bool {
	value, ok := tuiHistoryOptionValue(options, label)
	return ok && parseTUIBool(value)
}

func tuiHistoryOptionValue(options []tuiCommandHistoryOption, label string) (string, bool) {
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Label), strings.TrimSpace(label)) {
			return option.Value, true
		}
	}
	return "", false
}

func newTUICommandHistoryEntry(form tuiForm, command string, startedAt time.Time) tuiCommandHistoryEntry {
	return tuiCommandHistoryEntry{
		Title:     form.Title,
		Action:    form.Action,
		Options:   tuiFormHistoryOptions(form),
		Command:   command,
		StartedAt: startedAt,
	}
}

func tuiFormHistoryOptions(form tuiForm) []tuiCommandHistoryOption {
	options := make([]tuiCommandHistoryOption, 0, len(form.Fields))
	for _, field := range form.Fields {
		label := strings.TrimSpace(field.Label)
		if label == "" {
			label = field.Name
		}
		value := strings.TrimSpace(field.Value)
		if field.Input.Width > 0 {
			value = strings.TrimSpace(field.Input.Value())
		}
		options = append(options, tuiCommandHistoryOption{
			Label: label,
			Value: value,
		})
	}
	return options
}

func (m *tuiDashboardModel) finishHistoryEntry(msg tuiActionDoneMsg) {
	if m == nil || msg.History < 0 || msg.History >= len(m.history) {
		return
	}
	entry := &m.history[msg.History]
	entry.FinishedAt = msg.FinishedAt
	if entry.FinishedAt.IsZero() {
		entry.FinishedAt = time.Now()
	}
	entry.Code = msg.Code
	entry.StdoutBytes = len([]byte(msg.Stdout))
	entry.StderrBytes = len([]byte(msg.Stderr))
	entry.Canceled = msg.Canceled
	if msg.Err != nil {
		entry.Error = msg.Err.Error()
	} else if msg.Code != 0 {
		entry.Error = fmt.Sprintf("exit status %d", msg.Code)
	}
	if msg.CancelEvent != nil {
		entry.CancelEventPath = msg.CancelEvent.EventPath
		entry.UpdatedCandidates = append([]string(nil), msg.CancelEvent.UpdatedCandidates...)
	}
	if msg.CancelErr != nil {
		entry.CancelError = msg.CancelErr.Error()
	}
}

func (entry tuiCommandHistoryEntry) status() string {
	if entry.FinishedAt.IsZero() {
		return "(running)"
	}
	if entry.Canceled {
		return "(canceled)"
	}
	if strings.TrimSpace(entry.Error) != "" {
		return "(failed: " + entry.Error + ")"
	}
	return "(complete)"
}

func (m tuiDashboardModel) candidateDetail() string {
	if len(m.data.Results) == 0 {
		return ""
	}
	result := m.data.Results[m.selected]
	scoreFormatter := newDynamicFloatFormatter([]float64{result.Score}, 2, 4)
	p95Formatter := newDynamicFloatFormatter([]float64{result.Metrics.P95LatencyMS}, 2, 6)
	nsPerOpFormatter := newDynamicFloatFormatter([]float64{result.Metrics.BenchmarkNsPerOp}, 0, 4)

	var b strings.Builder
	fmt.Fprintf(&b, "Candidate Detail\n")
	fmt.Fprintf(&b, "ID: %s  Status: %s  Score: %s\n", result.Candidate.ID, result.Status, scoreFormatter.format(result.Score))
	fmt.Fprintf(&b, "Name: %s\n", displayValue(result.Candidate.Name))
	fmt.Fprintf(&b, "Round: %d  Agent: %s  Model: %s\n", result.Candidate.Round, displayValue(result.Candidate.Agent), displayValue(result.Candidate.Model))
	fmt.Fprintf(&b, "Source: %s\n", displayValue(result.Candidate.SourcePath))
	fmt.Fprintf(&b, "P95: %s ms  ns/op: %s  Memory: %s  Eval CPU: %s s\n",
		formatOptionalFloat(p95Formatter, result.Metrics.P95LatencyMS),
		formatOptionalFloat(nsPerOpFormatter, result.Metrics.BenchmarkNsPerOp),
		formatBytes(result.Metrics.MemoryPeakBytes),
		formatOptionalCPU(result.Metrics.CPUUserSeconds+result.Metrics.CPUSystemSeconds),
	)
	fmt.Fprintf(&b, "External: %d calls  Policy: %s\n", leaderboardExternalCallCount(result), displayValue(externalPolicyStatus(result)))
	if result.ScoreExplanation != nil && strings.TrimSpace(result.ScoreExplanation.Reason) != "" {
		fmt.Fprintf(&b, "Score note: %s\n", result.ScoreExplanation.Reason)
	}
	if len(result.Verdict.Errors) > 0 {
		fmt.Fprintf(&b, "Error: %s\n", result.Verdict.Errors[0])
	}
	if len(result.Verdict.Warnings) > 0 {
		fmt.Fprintf(&b, "Warning: %s\n", result.Verdict.Warnings[0])
	}
	if len(result.Verdict.Notes) > 0 {
		fmt.Fprintf(&b, "Note: %s\n", result.Verdict.Notes[0])
	}
	return b.String()
}

func (m tuiDashboardModel) nextActions() []string {
	runID := m.data.Config.ID
	if strings.TrimSpace(runID) == "" {
		return []string{
			"[S] Start tournament: type the request, Enter starts the full tournament",
			"[?] Advanced: Discovery only and lower-frequency tools",
		}
	}
	if m.needsEvaluator() {
		actions := []string{
			"[T] Create test harness: scoring needs an evaluator first",
			"[G] Create candidates after the harness exists",
			"[S] Start new tournament",
			"[H] Command history",
			"[?] Advanced tools",
		}
		if len(m.data.Results) > 0 {
			selected := clampInt(m.selected, 0, len(m.data.Results)-1)
			actions = append(actions, fmt.Sprintf("[R] Review selected candidate (%s)", m.data.Results[selected].Candidate.ID))
		}
		return actions
	}
	actions := []string{
		"[S] Start new tournament",
		"[G] Create more candidates",
		"[T] Test & score candidates",
		"[C] Continue tournament",
		"[E] Export report",
		"[H] Command history",
		"[?] Advanced tools",
	}
	if len(m.data.Results) > 0 {
		candidateID := m.data.Results[m.selected].Candidate.ID
		actions = append(actions, fmt.Sprintf("[R] Review selected candidate (%s)", candidateID))
		if !m.selectedCandidateIsBaseline() {
			actions = append(actions, fmt.Sprintf("[A] Apply selected candidate (%s)", candidateID))
		}
	}
	return actions
}

func (m tuiDashboardModel) dashboardKeyHelp() string {
	if !m.hasLoadedRun() {
		return "\nKeys: [S] start, [?] advanced, [Q] quit\n"
	}
	if m.needsEvaluator() {
		if len(m.data.Results) > 0 {
			return "\nKeys: [T] create test harness, [G] create candidates, [R] review\n" +
				"      [S] start, [H] history, [?] advanced, [Q] quit\n"
		}
		return "\nKeys: [T] create test harness, [S] start, [H] history, [?] advanced, [Q] quit\n"
	}
	if m.selectedCandidateIsBaseline() {
		return "\nKeys: [S] start, [R] review, [C] continue, [T] test\n" +
			"      [G] create candidates, [E] export, [H] history, [?] advanced, [Q] quit\n"
	}
	return "\nKeys: [S] start, [R] review, [A] apply, [C] continue, [T] test\n" +
		"      [G] create candidates, [E] export, [H] history, [?] advanced, [Q] quit\n"
}

func (m tuiDashboardModel) advancedActions() []tuiAdvancedAction {
	if !m.hasLoadedRun() {
		return []tuiAdvancedAction{
			{
				Label:       "Discovery only",
				Description: "Find likely source files and interfaces without starting a tournament.",
				Action:      tuiActionDiscover,
			},
		}
	}
	actions := []tuiAdvancedAction{
		{
			Label:       "Create test harness",
			Description: "Generate or refresh the evaluator used to test candidate behavior and metrics.",
			Action:      tuiActionEvaluator,
		},
		{
			Label:       "Import generated candidates",
			Description: "Add candidate directories already written into the current round to the leaderboard.",
			Action:      tuiActionAdopt,
		},
		{
			Label:       "Create next round",
			Description: "Prepare the next round from the best current candidates without running generation.",
			Action:      tuiActionNextRound,
		},
		{
			Label:       "Refresh archive index",
			Description: "Rebuild the local SQLite index from .crucible run artifacts.",
			Action:      tuiActionIndex,
		},
		{
			Label:       "Browse archive",
			Description: "List archived runs or candidates for inspection and automation.",
			Action:      tuiActionQuery,
		},
		{
			Label:       "Discovery only",
			Description: "Create a source/interface discovery plan without changing this tournament.",
			Action:      tuiActionDiscover,
		},
	}
	return actions
}

func (m tuiDashboardModel) hasLoadedRun() bool {
	return strings.TrimSpace(m.data.Config.ID) != ""
}

func (m tuiDashboardModel) selectedCandidateID() string {
	if len(m.data.Results) == 0 {
		return ""
	}
	selected := clampInt(m.selected, 0, len(m.data.Results)-1)
	return m.data.Results[selected].Candidate.ID
}

func (m tuiDashboardModel) selectedCandidateIsBaseline() bool {
	if len(m.data.Results) == 0 {
		return false
	}
	selected := clampInt(m.selected, 0, len(m.data.Results)-1)
	return m.data.Results[selected].Candidate.Baseline
}

func (m tuiDashboardModel) needsEvaluator() bool {
	needsEvaluator := false
	for _, result := range m.data.Results {
		if result.Status == model.CandidateStatusNeedsEvaluator {
			needsEvaluator = true
		}
		if result.Status == model.CandidateStatusPassed || result.Status == model.CandidateStatusFailed {
			return false
		}
	}
	return needsEvaluator
}

func defaultCandidateSelectionIndex(results []model.CandidateResult) int {
	for i, result := range results {
		if result.Status == model.CandidateStatusPassed && !result.Candidate.Baseline {
			return i
		}
	}
	for i, result := range results {
		if !result.Candidate.Baseline {
			return i
		}
	}
	return 0
}

func tuiStatusCounts(results []model.CandidateResult) (passed, failed, canceled, pending int) {
	for _, result := range results {
		switch result.Status {
		case model.CandidateStatusPassed:
			passed++
		case model.CandidateStatusFailed:
			failed++
		case model.CandidateStatusCanceled:
			canceled++
		default:
			pending++
		}
	}
	return passed, failed, canceled, pending
}

func externalPolicyStatus(result model.CandidateResult) string {
	if result.External.PolicyEnforcement != nil && strings.TrimSpace(result.External.PolicyEnforcement.Status) != "" {
		return result.External.PolicyEnforcement.Status
	}
	if result.External.PolicyPassed {
		return "passed"
	}
	if result.External.Mode != "" {
		return string(result.External.Mode)
	}
	return ""
}

func displayValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func formatOptionalCPU(value float64) string {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "-"
	}
	return newDynamicFloatFormatter([]float64{value}, 2, 4).format(value)
}

func parsePositiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseTUIBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func clampInt(value, minValue, maxValue int) int {
	if maxValue < minValue {
		return minValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n'\"\\$&;()<>|*?![]{}") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
