// Package projectscaffold prepares explicit new-project sources. Model
// generation and publication continue to belong to their existing owners.
package projectscaffold

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/progresshans/godj/version"
)

//go:embed templates
var projectTemplates embed.FS

// Project declares an explicit module dependency. A development source is a
// caller-selected physical path; it is never inferred from the installed tool.
type Project struct {
	Module           string
	FrameworkVersion string
	FrameworkSource  string
}

// File is one caller-owned source document, before model generation. No
// generated ABI bytes or private publication metadata are fabricated here.
type File struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

func (Project) Format(state fmt.State, _ rune) { fmt.Fprint(state, "scaffold.Project{redacted}") }

var fixedVersion = regexp.MustCompile(`^v(?:0|1)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

// ValidateModule rejects source/path injection before the command resolves a
// working directory. The Go toolchain performs final module-path validation.
func ValidateModule(module string) error {
	if !validModule(module) || module == version.Module {
		return fmt.Errorf("scaffold: invalid project module")
	}
	return nil
}

// RenderProject validates all names before reading templates and returns
// deterministic editable source. The caller must still resolve dependencies,
// generate the actual model bundle and verify compilation before publication.
func RenderProject(project Project) ([]File, error) {
	if err := ValidateModule(project.Module); err != nil {
		return nil, err
	}
	if project.FrameworkSource != "" {
		if project.FrameworkVersion != "" || !filepath.IsAbs(project.FrameworkSource) || strings.ContainsAny(project.FrameworkSource, "\x00\r\n") {
			return nil, fmt.Errorf("scaffold: invalid development dependency")
		}
	} else if len(project.FrameworkVersion) > 128 || !fixedVersion.MatchString(project.FrameworkVersion) {
		return nil, fmt.Errorf("scaffold: explicit framework dependency required")
	}
	var files []File
	err := fs.WalkDir(projectTemplates, "templates", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !strings.HasSuffix(name, ".tmpl") {
			return fmt.Errorf("scaffold: invalid embedded template")
		}
		source, err := projectTemplates.ReadFile(name)
		if err != nil {
			return err
		}
		parsed, err := template.New(name).Delims("[[", "]]").Option("missingkey=error").Parse(string(source))
		if err != nil {
			return fmt.Errorf("scaffold: invalid embedded template")
		}
		var output bytes.Buffer
		if err := parsed.Execute(&output, project); err != nil {
			return fmt.Errorf("scaffold: template rendering failed")
		}
		relative := strings.TrimSuffix(strings.TrimPrefix(name, "templates/"), ".tmpl")
		if relative == "gitignore" {
			relative = ".gitignore"
		}
		contents := output.Bytes()
		if path.Ext(relative) == ".go" {
			contents, err = format.Source(contents)
			if err != nil {
				return fmt.Errorf("scaffold: rendered Go source is invalid")
			}
		}
		files = append(files, File{Path: relative, Data: bytes.Clone(contents), Mode: 0o644})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func validModule(module string) bool {
	if module == "" || len(module) > 512 {
		return false
	}
	for _, component := range strings.Split(module, "/") {
		if component == "" || component[0] == '.' || strings.HasSuffix(component, ".") {
			return false
		}
		for _, c := range component {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' || c == '~') {
				return false
			}
		}
	}
	return true
}
