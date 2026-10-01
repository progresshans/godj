package consumertest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// A failed child may identify only an exact static fail("stage") from the
// checked-in consumer. Unknown errors, extra output and runtime data remain
// redacted by the command runner. Never expose transport errors or input URLs.
func consumerFailureStage(stderr []byte) string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(source), "testdata", "client", "cmd", "consumer", "*.go"))
	if err != nil {
		return ""
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return ""
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return ""
		}
		matched := ""
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "fail" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			stage, err := strconv.Unquote(literal.Value)
			if err == nil && string(stderr) == "consumer check failed: "+stage+"\n" {
				matched = stage
			}
			return true
		})
		if matched != "" {
			return matched
		}
	}
	return ""
}

func TestConsumerFailureDiagnosticsExposeOnlyKnownStaticStages(t *testing.T) {
	const stage = "generated report create defaults"
	if got := consumerFailureStage([]byte("consumer check failed: " + stage + "\n")); got != stage {
		t.Fatal("known failure hidden", got)
	}
	for _, input := range []string{"private runtime URL and token", "consumer check failed: unknown private value\n", "consumer check failed: " + stage + "\nprivate token", "consumer check failed: " + stage + " and private token\n"} {
		if got := consumerFailureStage([]byte(input)); got != "" {
			t.Fatal("untrusted diagnostic exposed")
		}
	}
}
