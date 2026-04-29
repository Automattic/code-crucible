package run

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
)

type PromotionOptions struct {
	ProjectDir    string
	RunID         string
	CandidateID   string
	DryRun        bool
	AllowUnpassed bool
}

type PromotionFile struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

type PromotionReport struct {
	RunID               string          `json:"run_id"`
	CandidateID         string          `json:"candidate_id"`
	CandidateStatus     string          `json:"candidate_status"`
	TargetPath          string          `json:"target_path"`
	CandidateSourcePath string          `json:"candidate_source_path"`
	DryRun              bool            `json:"dry_run"`
	Files               []PromotionFile `json:"files"`
	ReportPath          string          `json:"report_path,omitempty"`
	PromotedAt          time.Time       `json:"promoted_at,omitempty"`
}

type promotionPlanFile struct {
	source      string
	destination string
	mode        os.FileMode
}

func PromoteCandidate(opts PromotionOptions) (*PromotionReport, error) {
	projectDir := opts.ProjectDir
	if strings.TrimSpace(projectDir) == "" {
		projectDir = "."
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	configPath, err := archive.RunConfigPath(absProject, opts.RunID)
	if err != nil {
		return nil, err
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		return nil, err
	}
	runDir := cfg.RunDir
	if strings.TrimSpace(runDir) == "" {
		runDir = filepath.Dir(configPath)
	} else {
		runDir = archive.ProjectPath(absProject, runDir)
	}
	if strings.TrimSpace(cfg.SourcePath) == "" {
		return nil, fmt.Errorf("run %s has no source_path; create the run with --source-path before promoting candidates", cfg.ID)
	}

	board, err := archive.LoadLeaderboard(filepath.Join(runDir, "leaderboard.json"))
	if err != nil {
		return nil, err
	}
	result, err := promotionCandidate(board.Results, opts.CandidateID, opts.AllowUnpassed)
	if err != nil {
		return nil, err
	}

	candidateSource := archive.ProjectPath(absProject, result.Candidate.SourcePath)
	target := archive.ProjectPath(absProject, cfg.SourcePath)
	if err := validatePromotionTarget(absProject, target); err != nil {
		return nil, err
	}

	files, err := buildPromotionPlan(candidateSource, target)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("candidate %s did not provide any regular files to promote", result.Candidate.ID)
	}
	for _, file := range files {
		if err := validatePromotionDestination(absProject, file.destination); err != nil {
			return nil, err
		}
	}

	report := &PromotionReport{
		RunID:               cfg.ID,
		CandidateID:         result.Candidate.ID,
		CandidateStatus:     result.Status,
		TargetPath:          archive.ProjectRelativePath(absProject, target),
		CandidateSourcePath: archive.ProjectRelativePath(absProject, candidateSource),
		DryRun:              opts.DryRun,
		Files:               promotionReportFiles(absProject, files),
	}
	if opts.DryRun {
		return report, nil
	}

	for _, file := range files {
		if err := copyPromotionFile(file.source, file.destination, file.mode); err != nil {
			return nil, err
		}
	}

	report.PromotedAt = time.Now().UTC()
	reportPath := filepath.Join(runDir, "promotions", fmt.Sprintf("%s-%s.json", report.PromotedAt.Format("20060102-150405"), result.Candidate.ID))
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return nil, err
	}
	report.ReportPath = archive.ProjectRelativePath(absProject, reportPath)
	if err := archive.SaveJSON(reportPath, report); err != nil {
		return nil, err
	}
	return report, nil
}

func promotionCandidate(results []model.CandidateResult, candidateID string, allowUnpassed bool) (model.CandidateResult, error) {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID != "" {
		for _, result := range results {
			if result.Candidate.ID != candidateID {
				continue
			}
			if result.Status != model.CandidateStatusPassed && !allowUnpassed {
				return model.CandidateResult{}, fmt.Errorf("candidate %s has status %q; rerun evaluation or pass --allow-unpassed to promote anyway", candidateID, result.Status)
			}
			return result, nil
		}
		return model.CandidateResult{}, fmt.Errorf("candidate %q was not found in leaderboard", candidateID)
	}

	ranked := append([]model.CandidateResult(nil), results...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left := ranked[i]
		right := ranked[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if left.Metrics.P95LatencyMS != right.Metrics.P95LatencyMS {
			return left.Metrics.P95LatencyMS < right.Metrics.P95LatencyMS
		}
		return left.Candidate.ID < right.Candidate.ID
	})
	for _, result := range ranked {
		if result.Status == model.CandidateStatusPassed && !result.Candidate.Baseline {
			return result, nil
		}
	}
	return model.CandidateResult{}, fmt.Errorf("no passing non-baseline candidate was found; pass --candidate to promote a specific candidate")
}

func validatePromotionTarget(projectDir, target string) error {
	cleanProject := filepath.Clean(projectDir)
	cleanTarget := filepath.Clean(target)
	if !pathWithin(cleanTarget, cleanProject) {
		return fmt.Errorf("promotion target %s is outside project %s", cleanTarget, cleanProject)
	}
	workDir := filepath.Clean(project.WorkDir(cleanProject))
	if cleanTarget == workDir || strings.HasPrefix(cleanTarget, workDir+string(filepath.Separator)) {
		return fmt.Errorf("promotion target %s is inside the Code Crucible work area", cleanTarget)
	}
	return nil
}

func buildPromotionPlan(candidateSource, target string) ([]promotionPlanFile, error) {
	sourceInfo, err := os.Lstat(candidateSource)
	if err != nil {
		return nil, fmt.Errorf("candidate source missing: %w", err)
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("candidate source %s is a symlink; promotion only copies regular files and directories", candidateSource)
	}
	targetInfo, err := os.Lstat(target)
	if err != nil {
		return nil, fmt.Errorf("promotion target missing: %w", err)
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("promotion target %s is a symlink; promotion only writes directly inside the project", target)
	}

	if !targetInfo.IsDir() {
		sourceFile := candidateSource
		if sourceInfo.IsDir() {
			sourceFile = filepath.Join(candidateSource, filepath.Base(target))
		}
		info, err := promotionSourceFileInfo(sourceFile)
		if err != nil {
			return nil, err
		}
		return []promotionPlanFile{{source: sourceFile, destination: target, mode: info.Mode().Perm()}}, nil
	}
	if !sourceInfo.IsDir() {
		return nil, fmt.Errorf("candidate source %s is a file but promotion target %s is a directory", candidateSource, target)
	}

	files := []promotionPlanFile{}
	err = filepath.WalkDir(candidateSource, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("candidate source contains symlink %s; promotion only copies regular files", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("candidate source contains non-regular file %s", path)
		}
		rel, err := filepath.Rel(candidateSource, path)
		if err != nil {
			return err
		}
		files = append(files, promotionPlanFile{
			source:      path,
			destination: filepath.Join(target, rel),
			mode:        info.Mode().Perm(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].destination < files[j].destination
	})
	return files, nil
}

func promotionSourceFileInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("candidate source file missing: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("candidate source file %s is a symlink; promotion only copies regular files", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("candidate source file %s is not a regular file", path)
	}
	return info, nil
}

func validatePromotionDestination(projectDir, destination string) error {
	if err := validatePromotionTarget(projectDir, destination); err != nil {
		return err
	}
	cleanProject := filepath.Clean(projectDir)
	cleanDestination := filepath.Clean(destination)
	rel, err := filepath.Rel(cleanProject, cleanDestination)
	if err != nil {
		return err
	}
	current := cleanProject
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("promotion destination %s traverses symlink %s", cleanDestination, current)
		}
	}
	return nil
}

func promotionReportFiles(projectDir string, files []promotionPlanFile) []PromotionFile {
	out := make([]PromotionFile, 0, len(files))
	for _, file := range files {
		out = append(out, PromotionFile{
			Source:      archive.ProjectRelativePath(projectDir, file.source),
			Destination: archive.ProjectRelativePath(projectDir, file.destination),
		})
	}
	return out
}

func copyPromotionFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return out.Close()
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
