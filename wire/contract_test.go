package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// TestCommandConstantsAreRegistered parses the package source and verifies that
// every exported ControlCmd/DataCmd/EmptyCmd constant has a matching entry in
// the command table. This is the CI backstop for "new command, forgot the
// table": adding a constant without registering it fails the build.
func TestCommandConstantsAreRegistered(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[Cmd]string)
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				decl, ok := n.(*ast.GenDecl)
				if !ok || decl.Tok != token.CONST {
					return true
				}
				for _, spec := range decl.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || vs.Type == nil {
						continue
					}
					typeIdent, ok := vs.Type.(*ast.Ident)
					if !ok {
						continue
					}
					if !isCommandType(typeIdent.Name) {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						lit, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.INT {
							t.Fatalf("%s: value is not an integer literal", name.Name)
						}
						v, err := strconv.ParseUint(lit.Value, 0, 8)
						if err != nil {
							t.Fatalf("%s: %v", name.Name, err)
						}
						cmd := Cmd(v)
						class, ok := ClassOf(cmd)
						if !ok {
							t.Fatalf("%s (0x%02x) is not in the command table", name.Name, v)
						}
						switch {
						case name.Name == "CmdAdmit":
							if class != ClassHandshake {
								t.Fatalf("CmdAdmit class=%s, want handshake", class)
							}
						case typeIdent.Name == "ControlCmd":
							if class != ClassControl {
								t.Fatalf("%s class=%s, want control", name.Name, class)
							}
						case typeIdent.Name == "DataCmd":
							if class != ClassData {
								t.Fatalf("%s class=%s, want data", name.Name, class)
							}
						case typeIdent.Name == "EmptyCmd":
							if class != ClassControl && class != ClassData {
								t.Fatalf("%s class=%s, empty command must be control or data", name.Name, class)
							}
							if !noPayload(cmd) {
								t.Fatalf("%s is typed EmptyCmd but the table does not mark it no-payload", name.Name)
							}
						}
						if prev, dup := seen[cmd]; dup {
							t.Fatalf("%s and %s share command 0x%02x", prev, name.Name, v)
						}
						seen[cmd] = name.Name
					}
				}
				return true
			})
		}
	}

	if len(seen) != len(AllCommands()) {
		t.Fatalf("typed constants=%d, command table=%d", len(seen), len(AllCommands()))
	}
	for _, cmd := range AllCommands() {
		if _, ok := seen[cmd]; !ok {
			t.Fatalf("command table has 0x%02x (%s) without a typed constant", byte(cmd), cmd)
		}
	}
}

func isCommandType(typeName string) bool {
	switch typeName {
	case "ControlCmd", "DataCmd", "EmptyCmd":
		return true
	default:
		return false
	}
}
