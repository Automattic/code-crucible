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
	spinner      spinner.Model
	table        table.Model
	detail       viewport.Model
	result       viewport.Model
}

type tuiMode int

const (
	tuiModeDashboard tuiMode = iota
	tuiModeForm
	tuiModeActionResult
)

type tuiAction string

const (
	tuiActionRun      tuiAction = "run"
	tuiActionDiscover tuiAction = "discover"
	tuiActionGenerate tuiAction = "generate"
	tuiActionEvaluate tuiAction = "evaluate"
	tuiActionReport   tuiAction = "report"
	tuiActionQuery    tuiAction = "query"
)

type tuiForm struct {
	Action  tuiAction
	Title   string
	Fields  []tuiFormField
	Focus   int
	Message string
}

type tuiFormField struct {
	Name     string
	Label    string
	Value    string
	Required bool
	Input    textinput.Model
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

	data, message, err := loadInitialTUIDashboard(*projectDir, *runID)
	if err != nil {
		fmt.Fprintf(stderr, "tui failed: %v\n", err)
		return 1
	}

	programOptions := []tea.ProgramOption{
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
	}
	if !*noAltScreen {
		programOptions = append(programOptions, tea.WithAltScreen())
	}
	program := tea.NewProgram(newTUIDashboardModel(data, message), programOptions...)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(stderr, "tui failed: %v\n", err)
		return 1
	}
	return 0
}

func loadInitialTUIDashboard(projectDir, runSelector string) (tuiDashboardData, string, error) {
	data, err := loadTUIDashboard(projectDir, runSelector)
	if err == nil {
		return data, "", nil
	}
	selector := strings.TrimSpace(runSelector)
	if selector != "" && selector != "latest" {
		return tuiDashboardData{}, "", err
	}
	absProject, absErr := filepath.Abs(defaultString(projectDir, "."))
	if absErr != nil {
		return tuiDashboardData{}, "", absErr
	}
	return tuiDashboardData{
		ProjectDir: absProject,
		Config: model.RunConfig{
			ID:       "",
			Variants: defaultVariantCount,
			External: model.ExternalPolicy{Mode: model.ExternalModeDeny},
		},
	}, fmt.Sprintf("No run loaded yet: %v", err), nil
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
		data:    data,
		width:   100,
		message: message,
		spinner: spinner.New(),
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
		m.mode = tuiModeActionResult
		m.actionTitle = msg.Title
		m.actionOutput = msg.Stdout
		m.actionError = msg.Stderr
		if msg.Canceled {
			m.message = fmt.Sprintf("%s canceled", msg.Title)
			if msg.CancelEvent != nil {
				if len(msg.CancelEvent.UpdatedCandidates) > 0 {
					m.message += fmt.Sprintf("; marked canceled: %s", strings.Join(msg.CancelEvent.UpdatedCandidates, ", "))
				}
				if strings.TrimSpace(msg.CancelEvent.EventPath) != "" {
					if strings.TrimSpace(m.actionOutput) != "" {
						m.actionOutput += "\n"
					}
					m.actionOutput += "Cancellation event: " + msg.CancelEvent.EventPath + "\n"
				}
			}
			if msg.CancelErr != nil {
				if strings.TrimSpace(m.actionError) != "" {
					m.actionError += "\n"
				}
				m.actionError += "Cancellation artifact update failed: " + msg.CancelErr.Error() + "\n"
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
		m.configureBubbles()
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case tea.KeyMsg:
		if m.busy {
			switch msg.String() {
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
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "b", "esc":
				m.mode = tuiModeDashboard
				return m, nil
			}
			var cmd tea.Cmd
			m.result, cmd = m.result.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "n":
			m.openForm(tuiActionRun)
		case "d":
			m.openForm(tuiActionDiscover)
		case "g":
			m.openForm(tuiActionGenerate)
		case "e":
			m.openForm(tuiActionEvaluate)
		case "r":
			m.openForm(tuiActionReport)
		case "s":
			m.openForm(tuiActionQuery)
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
		b.WriteString("\nNo run is loaded yet.\n\n")
	} else {
		fmt.Fprintf(&b, "Run: %s\n", m.data.Config.ID)
		fmt.Fprintf(&b, "Created: %s\n", m.data.Config.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
		fmt.Fprintf(&b, "Optimize: %s\n", shorten(m.data.Config.Optimize, maxInt(24, width-10)))
		fmt.Fprintf(&b, "Agent: %s  Round: %s  Variants: %d  External: %s\n",
			displayValue(m.data.Config.Agent),
			filepath.Base(filepath.FromSlash(m.data.Config.RoundDir)),
			m.data.Config.Variants,
			displayValue(string(m.data.Config.External.Mode)),
		)
		passed, failed, canceled, pending := tuiStatusCounts(m.data.Results)
		fmt.Fprintf(&b, "Candidates: %d  Passed: %d  Failed: %d  Canceled: %d  Pending: %d\n\n", len(m.data.Results), passed, failed, canceled, pending)
	}

	if len(m.data.Results) == 0 {
		b.WriteString("No candidates are archived for this run.\n\n")
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
	b.WriteString("\nKeys: n new run, d discover, g generate, e evaluate, r report, s query, j/k select, q quit\n")
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
	m.mode = tuiModeActionResult
	m.busy = true
	m.actionTitle = title
	m.actionOutput = ""
	m.actionError = ""
	m.actionStart = time.Now()
	m.actionCmd = form.commandPreview(projectDir, m.data.Config.ID)
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
	fmt.Fprintf(&b, "\nCommand Preview\n%s\n", m.form.commandPreview(m.data.ProjectDir, m.data.Config.ID))
	b.WriteString("\nKeys: type/edit, tab/down next field, up previous field, ctrl+u clear, enter run, esc cancel\n")
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

func (m tuiDashboardModel) actionProgressView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Code Crucible\n\n")
	fmt.Fprintf(&b, "%s Running %s\n", m.spinner.View(), displayValue(m.actionTitle))
	if !m.actionStart.IsZero() {
		fmt.Fprintf(&b, "Elapsed: %s\n", formatTUIDuration(time.Since(m.actionStart)))
	}
	if strings.TrimSpace(m.actionCmd) != "" {
		fmt.Fprintf(&b, "\nCommand\n%s\n", m.actionCmd)
	}
	b.WriteString("\nOutput will appear when the action finishes.\n")
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
	variants := m.data.Config.Variants
	if variants <= 0 {
		variants = defaultVariantCount
	}
	switch action {
	case tuiActionRun:
		return tuiForm{
			Action: action,
			Title:  "Create Run",
			Fields: []tuiFormField{
				{Name: "optimize", Label: "Optimization request", Required: true},
				{Name: "source_path", Label: "Source path"},
				{Name: "agent", Label: "Agent", Value: agent},
				{Name: "variants", Label: "Variants", Value: strconv.Itoa(variants)},
				{Name: "generate", Label: "Generate now", Value: "false"},
			},
		}
	case tuiActionDiscover:
		return tuiForm{
			Action: action,
			Title:  "Discover Source And Interfaces",
			Fields: []tuiFormField{
				{Name: "request", Label: "Optimization request", Required: true},
				{Name: "agent", Label: "Discovery agent", Value: "local"},
			},
		}
	case tuiActionGenerate:
		return tuiForm{
			Action: action,
			Title:  "Generate Competitors",
			Fields: []tuiFormField{
				{Name: "run", Label: "Run", Value: runID, Required: true},
				{Name: "agent", Label: "Agent", Value: agent},
			},
		}
	case tuiActionEvaluate:
		return tuiForm{
			Action: action,
			Title:  "Evaluate Candidates",
			Fields: []tuiFormField{
				{Name: "run", Label: "Run", Value: runID, Required: true},
				{Name: "candidate", Label: "Candidate filter"},
				{Name: "jobs", Label: "Jobs", Value: "1"},
				{Name: "external_routing", Label: "External routing"},
			},
		}
	case tuiActionReport:
		return tuiForm{
			Action: action,
			Title:  "Write Report",
			Fields: []tuiFormField{
				{Name: "run", Label: "Run", Value: runID, Required: true},
				{Name: "output", Label: "Output path"},
				{Name: "json", Label: "Print JSON", Value: "false"},
			},
		}
	case tuiActionQuery:
		return tuiForm{
			Action: action,
			Title:  "Query Archive",
			Fields: []tuiFormField{
				{Name: "kind", Label: "Kind", Value: "runs", Required: true},
				{Name: "limit", Label: "Limit", Value: "10"},
				{Name: "run", Label: "Run for candidates", Value: runID},
				{Name: "status", Label: "Candidate status"},
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
			SourcePath: form.value("source_path"),
			Agent:      form.value("agent"),
			Variants:   variants,
			Generate:   parseTUIBool(form.value("generate")),
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
	case tuiActionReport:
		return controller.Report(ReportWorkflowOptions{
			RunSelector: form.value("run"),
			OutputPath:  form.value("output"),
			JSON:        parseTUIBool(form.value("json")),
		})
	case tuiActionQuery:
		return controller.Query(QueryWorkflowOptions{
			Kind:        defaultString(form.value("kind"), "runs"),
			Limit:       parsePositiveInt(form.value("limit"), 10),
			RunSelector: form.value("run"),
			Status:      form.value("status"),
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
	case tuiActionGenerate, tuiActionEvaluate, tuiActionReport:
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
				return fmt.Errorf("Variants must be a number")
			}
		}
	case tuiActionEvaluate:
		if value := strings.TrimSpace(f.value("jobs")); value != "" {
			if _, err := strconv.Atoi(value); err != nil {
				return fmt.Errorf("Jobs must be a number")
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
		if agent := strings.TrimSpace(f.value("agent")); agent != "" {
			args = append(args, "--agent", shellQuote(agent))
		}
		if variants := strings.TrimSpace(f.value("variants")); variants != "" {
			args = append(args, "--variants", shellQuote(variants))
		}
		if parseTUIBool(f.value("generate")) {
			args = append(args, "--generate")
		}
		args = append(args, shellQuote(f.value("optimize")))
		return strings.Join(args, " ")
	case tuiActionDiscover:
		return fmt.Sprintf("crucible discover --project-dir %s --agent %s %s", project, shellQuote(defaultString(f.value("agent"), "local")), shellQuote(f.value("request")))
	case tuiActionGenerate:
		args := []string{"crucible", "generate", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if agent := strings.TrimSpace(f.value("agent")); agent != "" {
			args = append(args, "--agent", shellQuote(agent))
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
	case tuiActionReport:
		args := []string{"crucible", "report", "--project-dir", project, "--run", shellQuote(defaultString(f.value("run"), runID))}
		if output := strings.TrimSpace(f.value("output")); output != "" {
			args = append(args, "--output", shellQuote(output))
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
			input.Placeholder = field.Label
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

func (m *tuiDashboardModel) configureBubbles() {
	if m == nil {
		return
	}
	m.configureCandidateTable()
	m.syncDetailViewport()
	m.syncResultViewport()
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
	project := shellQuote(m.data.ProjectDir)
	if strings.TrimSpace(runID) == "" {
		return []string{
			"New run form:       press n",
			"Discovery form:     press d",
		}
	}
	actions := []string{
		fmt.Sprintf("Generate competitors: crucible generate --project-dir %s --run %s", project, shellQuote(runID)),
		fmt.Sprintf("Evaluate candidates:  crucible evaluate --project-dir %s --run %s", project, shellQuote(runID)),
		fmt.Sprintf("Open leaderboard:     crucible leaderboard --project-dir %s --run %s", project, shellQuote(runID)),
		fmt.Sprintf("Write HTML report:    crucible report --project-dir %s --run %s", project, shellQuote(runID)),
		fmt.Sprintf("Prepare next round:   crucible next-round --project-dir %s --run %s", project, shellQuote(runID)),
	}
	if len(m.data.Results) > 0 {
		candidateID := m.data.Results[m.selected].Candidate.ID
		actions = append(actions, fmt.Sprintf("Inspect selected:     crucible inspect --project-dir %s --run %s %s", project, shellQuote(runID), shellQuote(candidateID)))
	}
	return actions
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
