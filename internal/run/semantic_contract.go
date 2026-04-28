package run

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Automattic/code-crucible/internal/discovery"
)

func semanticContractErrors(runDir, baselineSrc, candidateSrc string, baselineExts, candidateExts map[string]bool) []string {
	if !baselineExts[".go"] || !candidateExts[".go"] {
		return nil
	}
	plan, err := discovery.LoadAgentPlan(filepath.Join(runDir, "docs", "agent-discovery.json"))
	if err != nil {
		return nil
	}
	functionName := dropInFunctionName(plan.DropInInterface)
	if functionName == "" {
		return nil
	}

	baselineSigs, err := goFunctionSignatures(baselineSrc)
	if err != nil {
		return []string{"candidate contract failed: baseline Go source could not be parsed: " + err.Error()}
	}
	baselineSig, ok := baselineSigs[functionName]
	if !ok {
		return nil
	}

	candidateSigs, err := goFunctionSignatures(candidateSrc)
	if err != nil {
		return []string{"candidate contract failed: candidate Go source could not be parsed: " + err.Error()}
	}
	candidateSig, ok := candidateSigs[functionName]
	if !ok {
		return []string{fmt.Sprintf("candidate contract failed: Go drop-in function %s is missing", functionName)}
	}
	if candidateSig != baselineSig {
		return []string{fmt.Sprintf("candidate contract failed: Go drop-in function %s signature changed: got %s, want %s", functionName, candidateSig, baselineSig)}
	}
	return nil
}

func goFunctionSignatures(root string) (map[string]string, error) {
	signatures := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "target", "dist", "build":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			signature, err := goFuncTypeSignature(fset, fn.Type)
			if err != nil {
				return err
			}
			signatures[fn.Name.Name] = signature
		}
		return nil
	})
	return signatures, err
}

func goFuncTypeSignature(fset *token.FileSet, fn *ast.FuncType) (string, error) {
	params, err := goFieldTypeList(fset, fn.Params)
	if err != nil {
		return "", err
	}
	results, err := goFieldTypeList(fset, fn.Results)
	if err != nil {
		return "", err
	}
	signature := "func(" + strings.Join(params, ", ") + ")"
	if len(results) == 1 {
		signature += " " + results[0]
	} else if len(results) > 1 {
		signature += " (" + strings.Join(results, ", ") + ")"
	}
	return signature, nil
}

func goFieldTypeList(fset *token.FileSet, fields *ast.FieldList) ([]string, error) {
	if fields == nil {
		return nil, nil
	}
	out := make([]string, 0, len(fields.List))
	for _, field := range fields.List {
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, field.Type); err != nil {
			return nil, err
		}
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			out = append(out, buf.String())
		}
	}
	return out, nil
}

func dropInFunctionName(dropInInterface string) string {
	text := strings.TrimSpace(dropInInterface)
	text = strings.TrimPrefix(text, "func ")
	if text == "" {
		return ""
	}
	if idx := strings.Index(text, "("); idx >= 0 {
		text = text[:idx]
	} else if fields := strings.Fields(text); len(fields) > 0 {
		text = fields[0]
	}
	text = strings.TrimSpace(text)
	if idx := strings.LastIndex(text, "."); idx >= 0 {
		text = text[idx+1:]
	}
	return trimIdentifier(text)
}

func trimIdentifier(value string) string {
	value = strings.TrimSpace(value)
	start := -1
	end := -1
	for i, r := range value {
		if start == -1 {
			if r == '_' || unicode.IsLetter(r) {
				start = i
				end = i + len(string(r))
			}
			continue
		}
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			end = i + len(string(r))
			continue
		}
		break
	}
	if start == -1 {
		return ""
	}
	return value[start:end]
}
