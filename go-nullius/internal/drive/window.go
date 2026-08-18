package drive

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// EnclosingWindow returns the source of the smallest function/method
// enclosing line in file, plus its 1-based line range. RULE sees only this
// mechanically-extracted window, never the whole file — judge and refute
// reason within ONE function and can never cite a sibling function as
// evidence (DESIGN-mandates.md §4; the cautionary tale in go-nullius's
// judge.go enclosingWindow). Falls back to a ±windowRadius line window when
// no enclosing function is found (e.g. a package-level var, a non-Go file).
const windowRadius = 8

func EnclosingWindow(file string, line int) (text string, start, end int, err error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return "", 0, 0, fmt.Errorf("drive: read %s: %w", file, err)
	}
	lines := strings.Split(string(src), "\n")

	if strings.HasSuffix(file, ".go") {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, file, src, parser.ParseComments)
		if perr == nil {
			bestSpan := 1 << 30
			bestStart, bestEnd := 0, 0
			ast.Inspect(f, func(n ast.Node) bool {
				fd, ok := n.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					return true
				}
				s := fset.Position(fd.Pos()).Line
				e := fset.Position(fd.End()).Line
				if s <= line && line <= e && e-s < bestSpan {
					bestSpan, bestStart, bestEnd = e-s, s, e
				}
				return true
			})
			if bestSpan != 1<<30 {
				return renderRange(lines, bestStart, bestEnd), bestStart, bestEnd, nil
			}
		}
	}
	return renderWindow(lines, line, windowRadius)
}

func renderWindow(lines []string, line, radius int) (string, int, int, error) {
	start, end := line-radius, line+radius
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	return renderRange(lines, start, end), start, end, nil
}

func renderRange(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		if i-1 < 0 || i-1 >= len(lines) {
			continue
		}
		fmt.Fprintf(&b, "%d: %s\n", i, lines[i-1])
	}
	return b.String()
}

// HasFuncDecl reports whether file contains at least one Go function/method
// declaration — the mechanical GATE backstop: any pre-existing fn in scope
// forces FIX mode regardless of what the model claims
// (DESIGN-mandates.md §4 GATE fail-closed default).
func HasFuncDecl(file string) bool {
	if !strings.HasSuffix(file, ".go") {
		return false
	}
	src, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		return false
	}
	for _, d := range f.Decls {
		if _, ok := d.(*ast.FuncDecl); ok {
			return true
		}
	}
	return false
}
