package consumertest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A failed child may identify only an exact static fail("stage") from the
// checked-in consumer, optionally followed by one fixed context-state line.
// Unknown errors, extra output and runtime data remain
// redacted by the command runner. Never expose transport errors or input URLs.
func consumerFailureStage(stderr []byte) string {
	diagnostic := string(stderr)
	contextState := ""
	for _, state := range []string{"deadline exceeded", "canceled"} {
		if before, found := strings.CutSuffix(diagnostic, "consumer context: "+state+"\n"); found {
			diagnostic = before
			contextState = " (consumer context: " + state + ")"
			break
		}
	}
	paths, err := filepath.Glob(filepath.Join("testdata", "client", "cmd", "consumer", "*.go"))
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
			if err == nil && diagnostic == "consumer check failed: "+stage+"\n" {
				matched = stage + contextState
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
	for _, state := range []string{"deadline exceeded", "canceled"} {
		if got := consumerFailureStage([]byte("consumer check failed: " + stage + "\nconsumer context: " + state + "\n")); got != stage+" (consumer context: "+state+")" {
			t.Fatal("known context state hidden", got)
		}
	}
	for _, input := range []string{
		"private runtime URL and token",
		"consumer check failed: unknown private value\n",
		"consumer check failed: " + stage + "\nprivate token",
		"consumer check failed: " + stage + " and private token\n",
		"consumer check failed: unknown private value\nconsumer context: deadline exceeded\n",
		"consumer check failed: " + stage + "\nconsumer context: private token\n",
		"consumer check failed: " + stage + "\nconsumer context: deadline exceeded\nprivate token",
		"consumer check failed: " + stage + "\nconsumer context: deadline exceeded\nconsumer context: canceled\n",
		"consumer check failed: " + stage + "\nconsumer context: canceled",
	} {
		if got := consumerFailureStage([]byte(input)); got != "" {
			t.Fatal("untrusted diagnostic exposed")
		}
	}
}
