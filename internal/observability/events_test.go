package observability

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEveryEventHasProductionEmitterReference(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve observability test path")
	}
	packageDir := filepath.Dir(testFile)
	repositoryRoot := filepath.Clean(filepath.Join(packageDir, "..", ".."))
	eventNames := declaredEventNames(t, filepath.Join(packageDir, "events.go"))

	var productionSource bytes.Buffer
	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(repositoryRoot, root), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == testFile {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			productionSource.Write(contents)
			return nil
		})
		if err != nil {
			t.Fatalf("scan production Go source: %v", err)
		}
	}

	for _, eventName := range eventNames {
		selector := []byte("observability." + eventName)
		if !bytes.Contains(productionSource.Bytes(), selector) {
			t.Errorf("%s has no production emitter reference", eventName)
		}
	}
}

func declaredEventNames(t *testing.T, path string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse event catalog: %v", err)
	}
	var names []string
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range values.Names {
				if strings.HasPrefix(name.Name, "Event") {
					names = append(names, name.Name)
				}
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("event catalog declares no events")
	}
	return names
}
