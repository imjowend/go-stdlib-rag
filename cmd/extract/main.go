// Command extract walks the Go standard library source tree and writes one
// JSON object per documented, exported symbol to a JSONL file.
//
// It is meant to be run with the Go toolchain whose stdlib you want to
// document, e.g.:
//
//	go1.26.5 run ./cmd/extract
//
// so that go/doc and go/ast match the source being parsed.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
)

func main() {
	defaultGoroot := build.Default.GOROOT
	goroot := flag.String("goroot", defaultGoroot, "GOROOT whose src/ tree will be documented")
	out := flag.String("out", filepath.Join("data", "stdlib_docs.jsonl"), "output JSONL path")
	verbose := flag.Bool("v", false, "verbose: log each package processed")
	flag.Parse()

	srcDir := filepath.Join(*goroot, "src")
	if _, err := os.Stat(srcDir); err != nil {
		log.Fatalf("source tree not found at %s: %v", srcDir, err)
	}

	pkgDirs, err := findPackageDirs(srcDir)
	if err != nil {
		log.Fatalf("scanning %s: %v", srcDir, err)
	}
	log.Printf("found %d candidate stdlib packages under %s", len(pkgDirs), srcDir)

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("creating output dir: %v", err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatalf("creating %s: %v", *out, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)

	var totalSymbols, totalPkgs int
	for _, pd := range pkgDirs {
		syms, err := extractPackage(srcDir, pd)
		if err != nil {
			if *verbose {
				log.Printf("skip %s: %v", pd.importPath, err)
			}
			continue
		}
		if len(syms) == 0 {
			continue
		}
		for _, s := range syms {
			if err := enc.Encode(s); err != nil {
				log.Fatalf("writing symbol %s.%s: %v", s.Package, s.Name, err)
			}
		}
		totalSymbols += len(syms)
		totalPkgs++
		if *verbose {
			log.Printf("%-30s %d symbols", pd.importPath, len(syms))
		}
	}

	log.Printf("done: %d symbols from %d packages -> %s", totalSymbols, totalPkgs, *out)
}

// pkgDir represents a parsed Go package directory.
type pkgDir struct {
	dir        string // absolute directory
	importPath string // import path relative to src
}

// findPackageDirs walks srcDir and returns directories that look like
// importable stdlib packages, excluding internal/vendor/testdata/cmd.
func findPackageDirs(srcDir string) ([]pkgDir, error) {
	var dirs []pkgDir
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		base := d.Name()
		// Skip non-importable / noise directories and don't descend into them.
		if base == "internal" || base == "vendor" || base == "testdata" ||
			base == "cmd" || strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
			return fs.SkipDir
		}
		// Only record dirs that actually contain non-test Go files.
		hasGo, err := dirHasGoFiles(path)
		if err != nil {
			return err
		}
		if hasGo {
			dirs = append(dirs, pkgDir{
				dir:        path,
				importPath: filepath.ToSlash(rel),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].importPath < dirs[j].importPath })
	return dirs, nil
}

// dirHasGoFiles reports whether the directory contains buildable non-test Go files.
func dirHasGoFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true, nil
		}
	}
	return false, nil
}

// extractPackage parses one package directory and returns its documented,
// exported symbols with examples attached.
func extractPackage(srcDir string, pd pkgDir) ([]docmodel.Symbol, error) {
	// Use the default build context to filter files by GOOS/GOARCH and build tags.
	bpkg, err := build.ImportDir(pd.dir, 0)
	if err != nil {
		return nil, err
	}

	var fileNames []string
	fileNames = append(fileNames, bpkg.GoFiles...)
	fileNames = append(fileNames, bpkg.TestGoFiles...)
	fileNames = append(fileNames, bpkg.XTestGoFiles...)
	if len(fileNames) == 0 {
		return nil, fmt.Errorf("no buildable go files")
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range fileNames {
		full := filepath.Join(pd.dir, name)
		af, err := parser.ParseFile(fset, full, nil, parser.ParseComments)
		if err != nil {
			// Skip a single unparseable file rather than dropping the package.
			continue
		}
		files = append(files, af)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no parseable go files")
	}

	dpkg, err := doc.NewFromFiles(fset, files, pd.importPath)
	if err != nil {
		return nil, err
	}

	var syms []docmodel.Symbol

	// Package-level functions.
	for _, fn := range dpkg.Funcs {
		syms = append(syms, symbolFromFunc(fset, pd.importPath, "func", "", fn))
	}
	// Consts and vars declared at package level.
	for _, v := range dpkg.Consts {
		syms = append(syms, symbolFromValue(fset, pd.importPath, "const", v))
	}
	for _, v := range dpkg.Vars {
		syms = append(syms, symbolFromValue(fset, pd.importPath, "var", v))
	}
	// Types, plus their constructors, methods, and attached consts/vars.
	for _, t := range dpkg.Types {
		syms = append(syms, symbolFromType(fset, pd.importPath, t))
		for _, fn := range t.Funcs {
			syms = append(syms, symbolFromFunc(fset, pd.importPath, "func", "", fn))
		}
		for _, m := range t.Methods {
			syms = append(syms, symbolFromFunc(fset, pd.importPath, "method", t.Name, m))
		}
		for _, v := range t.Consts {
			syms = append(syms, symbolFromValue(fset, pd.importPath, "const", v))
		}
		for _, v := range t.Vars {
			syms = append(syms, symbolFromValue(fset, pd.importPath, "var", v))
		}
	}

	return syms, nil
}

func symbolFromFunc(fset *token.FileSet, pkg, kind, recv string, fn *doc.Func) docmodel.Symbol {
	return docmodel.Symbol{
		Package:   pkg,
		Kind:      kind,
		Name:      fn.Name,
		Recv:      recv,
		Signature: funcSignature(fset, fn.Decl),
		Doc:       strings.TrimSpace(fn.Doc),
		Examples:  convertExamples(fset, fn.Examples),
	}
}

func symbolFromType(fset *token.FileSet, pkg string, t *doc.Type) docmodel.Symbol {
	return docmodel.Symbol{
		Package:   pkg,
		Kind:      "type",
		Name:      t.Name,
		Signature: renderNode(fset, t.Decl),
		Doc:       strings.TrimSpace(t.Doc),
		Examples:  convertExamples(fset, t.Examples),
	}
}

func symbolFromValue(fset *token.FileSet, pkg, kind string, v *doc.Value) docmodel.Symbol {
	return docmodel.Symbol{
		Package:   pkg,
		Kind:      kind,
		Name:      strings.Join(v.Names, ", "),
		Signature: renderNode(fset, v.Decl),
		Doc:       strings.TrimSpace(v.Doc),
	}
}

// funcSignature renders a function/method declaration without its body.
func funcSignature(fset *token.FileSet, decl *ast.FuncDecl) string {
	if decl == nil {
		return ""
	}
	stripped := *decl
	stripped.Body = nil
	stripped.Doc = nil
	return renderNode(fset, &stripped)
}

// renderNode pretty-prints an AST node to Go source text.
func renderNode(fset *token.FileSet, node ast.Node) string {
	if node == nil {
		return ""
	}
	// Detach any leading doc comment from GenDecls so it isn't rendered twice.
	if gd, ok := node.(*ast.GenDecl); ok {
		cp := *gd
		cp.Doc = nil
		node = &cp
	}
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	if err := cfg.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	return buf.String()
}

// convertExamples transforms go/doc examples into the docmodel.Example format.
func convertExamples(fset *token.FileSet, exs []*doc.Example) []docmodel.Example {
	if len(exs) == 0 {
		return nil
	}
	out := make([]docmodel.Example, 0, len(exs))
	for _, ex := range exs {
		var node ast.Node = ex.Code
		if ex.Play != nil {
			node = ex.Play // full runnable file when available
		}
		code := renderNode(fset, node)
		if strings.TrimSpace(code) == "" {
			continue
		}
		out = append(out, docmodel.Example{
			Name:   ex.Suffix,
			Code:   code,
			Output: strings.TrimSpace(ex.Output),
		})
	}
	return out
}
