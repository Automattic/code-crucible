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
	"sort"
	"strings"
	"unicode"

	"github.com/Automattic/code-crucible/internal/discovery"
)

func semanticContractErrors(runDir, baselineSrc, candidateSrc string, baselineExts, candidateExts map[string]bool) []string {
	if !baselineExts[".go"] || !candidateExts[".go"] {
		return nil
	}

	baselineSigs, err := goCallableSignatures(baselineSrc)
	if err != nil {
		return []string{"candidate contract failed: baseline Go source could not be parsed: " + err.Error()}
	}
	candidateSigs, err := goCallableSignatures(candidateSrc)
	if err != nil {
		return []string{"candidate contract failed: candidate Go source could not be parsed: " + err.Error()}
	}

	var errors []string
	checked := map[string]bool{}
	if plan, err := discovery.LoadAgentPlan(filepath.Join(runDir, "docs", "agent-discovery.json")); err == nil {
		functionName := dropInFunctionName(plan.DropInInterface)
		if functionName != "" {
			if key, baselineSig, ok := findGoCallableSignature(baselineSigs, functionName); ok {
				checked[key] = true
				if candidateSig, ok := candidateSigs[key]; !ok {
					errors = append(errors, fmt.Sprintf("candidate contract failed: Go drop-in callable %s is missing", key))
				} else if candidateSig.Signature != baselineSig.Signature {
					errors = append(errors, fmt.Sprintf("candidate contract failed: Go drop-in callable %s signature changed: got %s, want %s", key, candidateSig.Signature, baselineSig.Signature))
				}
			}
		}
	}

	for key, baselineSig := range baselineSigs {
		if checked[key] || !baselineSig.Exported {
			continue
		}
		if candidateSig, ok := candidateSigs[key]; !ok {
			errors = append(errors, fmt.Sprintf("candidate contract failed: exported Go callable %s is missing", key))
		} else if candidateSig.Signature != baselineSig.Signature {
			errors = append(errors, fmt.Sprintf("candidate contract failed: exported Go callable %s signature changed: got %s, want %s", key, candidateSig.Signature, baselineSig.Signature))
		}
	}
	sort.Strings(errors)
	return errors
}

type goSignature struct {
	Signature string
	Exported  bool
}

func goCallableSignatures(root string) (map[string]goSignature, error) {
	signatures := map[string]goSignature{}
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
			if !ok {
				continue
			}
			signature, err := goFuncTypeSignature(fset, fn.Type)
			if err != nil {
				return err
			}
			if fn.Recv == nil {
				signatures[fn.Name.Name] = goSignature{
					Signature: signature,
					Exported:  ast.IsExported(fn.Name.Name),
				}
				continue
			}
			receiverType, receiverBase, err := goReceiverType(fset, fn.Recv)
			if err != nil {
				return err
			}
			if receiverBase == "" {
				continue
			}
			signatures[receiverBase+"."+fn.Name.Name] = goSignature{
				Signature: "method " + receiverType + " " + signature,
				Exported:  ast.IsExported(receiverBase) && ast.IsExported(fn.Name.Name),
			}
		}
		return nil
	})
	return signatures, err
}

func findGoCallableSignature(signatures map[string]goSignature, name string) (string, goSignature, bool) {
	if signature, ok := signatures[name]; ok {
		return name, signature, true
	}
	var matches []string
	for key := range signatures {
		if strings.HasSuffix(key, "."+name) {
			matches = append(matches, key)
		}
	}
	if len(matches) != 1 {
		return "", goSignature{}, false
	}
	return matches[0], signatures[matches[0]], true
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

func goReceiverType(fset *token.FileSet, fields *ast.FieldList) (string, string, error) {
	if fields == nil || len(fields.List) == 0 {
		return "", "", nil
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, fields.List[0].Type); err != nil {
		return "", "", err
	}
	receiverType := buf.String()
	return receiverType, receiverBaseName(receiverType), nil
}

func receiverBaseName(receiverType string) string {
	receiverType = strings.TrimSpace(strings.TrimPrefix(receiverType, "*"))
	if idx := strings.LastIndex(receiverType, "."); idx >= 0 {
		receiverType = receiverType[idx+1:]
	}
	if idx := strings.Index(receiverType, "["); idx >= 0 {
		receiverType = receiverType[:idx]
	}
	return trimIdentifier(receiverType)
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
