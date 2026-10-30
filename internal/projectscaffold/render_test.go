package projectscaffold

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScaffoldRenderRejectsSourceAndDependencyInjection(t *testing.T) {
	for _, module := range []string{"", "../escape", "example.com/../escape", "example.com//site", "example.com/site\nreplace private => /tmp", "example.com/\"site", "example.com/site\\child", "github.com/progresshans/godj"} {
		files, err := RenderProject(Project{Module: module, FrameworkVersion: "v0.1.0"})
		if err == nil || len(files) != 0 || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe module was rendered or exposed: %q", module)
		}
	}
	for _, project := range []Project{
		{Module: "example.com/site"},
		{Module: "example.com/site", FrameworkVersion: "latest"},
		{Module: "example.com/site", FrameworkVersion: "v2.0.0"},
		{Module: "example.com/site", FrameworkVersion: "v0.1.0\nreplace private => /tmp"},
		{Module: "example.com/site", FrameworkSource: "relative/private"},
		{Module: "example.com/site", FrameworkSource: "/private/source\nreplace"},
		{Module: "example.com/site", FrameworkSource: "/private/source", FrameworkVersion: "v0.1.0"},
	} {
		files, err := RenderProject(project)
		if err == nil || len(files) != 0 || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe dependency was rendered or exposed")
		}
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			if value := fmt.Sprintf(verb, project); strings.Contains(value, "private") {
				t.Fatal("source path leaked from diagnostic formatting")
			}
		}
	}
}

func TestScaffoldRenderingOwnsDeterministicEditableSource(t *testing.T) {
	project := Project{Module: "example.com/owned-site", FrameworkVersion: "v0.1.0-beta.1"}
	first, err := RenderProject(project)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderProject(project)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("the same declaration rendered different source", err)
	}
	names := make(map[string]bool)
	for index, file := range first {
		if names[file.Path] || !filepath.IsLocal(file.Path) || file.Mode != 0o644 || index > 0 && first[index-1].Path >= file.Path {
			t.Fatal("rendered path/mode/order is not a deterministic source set")
		}
		names[file.Path] = true
		if strings.HasPrefix(file.Path, ".godj/") || strings.Contains(file.Path, "zz_godj_") {
			t.Fatal("scaffold fabricated generation state")
		}
		if strings.HasSuffix(file.Path, ".go") {
			if _, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Data, parser.AllErrors); err != nil {
				t.Fatal("editable source did not parse", file.Path, err)
			}
		}
		if len(file.Data) == 0 {
			t.Fatal("empty source file")
		}
		file.Data[0] ^= 1
		if bytes.Equal(file.Data, second[index].Data) {
			t.Fatal("rendered source shares backing storage across calls")
		}
	}
	for _, name := range []string{"README.md", "go.mod", "godj.toml", "modeldef/apps.go", "cmd/projectrunner/main.go", "cmd/site/main.go", "cmd/commands/main.go", "internal/commands/commands.go", "internal/site/models.go"} {
		if !names[name] {
			t.Fatal("missing developer entrypoint", name)
		}
	}
	project.FrameworkVersion, project.FrameworkSource = "", "/explicit/source with spaces/quote\"end"
	files, err := RenderProject(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Path == "go.mod" && !bytes.Contains(file.Data, []byte(fmt.Sprintf("=> %q", project.FrameworkSource))) {
			t.Fatal("development source was not quoted as one module token")
		}
	}
}
