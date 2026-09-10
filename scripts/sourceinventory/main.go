// Command sourceinventory counts Git-selected Go and Python source lines.
// Generated Go is identified by go/ast.IsGenerated, never by a substring that
// may occur inside a handwritten generator's output template.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type source struct {
	Path      string `json:"path"`
	Language  string `json:"language"`
	Category  string `json:"category"`
	Lines     int    `json:"lines"`
	Generated bool   `json:"generated,omitempty"`
	SHA256    string `json:"sha256"`
}

type count struct {
	Files int `json:"files"`
	Lines int `json:"lines"`
}

type report struct {
	Revision     string           `json:"revision"`
	SourceDigest string           `json:"source_digest"`
	Go           map[string]count `json:"go"`
	Python       map[string]count `json:"python"`
	Sources      []source         `json:"sources"`
}

func main() {
	root := flag.String("root", ".", "Git checkout to inspect")
	revision := flag.String("revision", "", "commit/ref; default reads tracked and untracked non-ignored working files")
	flag.Parse()
	result, err := inventory(*root, *revision)
	if err == nil {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(result)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func git(root string, arguments ...string) ([]byte, error) {
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	document, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", arguments[0], err)
	}
	return document, nil
}

func inventory(root, revision string) (report, error) {
	result := report{Go: make(map[string]count), Python: make(map[string]count)}
	directory, err := git(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, err
	}
	root = strings.TrimSpace(string(directory))
	arguments := []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"}
	result.Revision = "working-tree"
	if revision != "" {
		commit, err := git(root, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
		if err != nil {
			return result, err
		}
		result.Revision = strings.TrimSpace(string(commit))
		arguments = []string{"ls-tree", "-rz", "--name-only", result.Revision}
	}
	listing, err := git(root, arguments...)
	if err != nil {
		return result, err
	}
	paths := make(map[string]bool)
	for _, path := range strings.Split(string(listing), "\x00") {
		if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".py") {
			paths[path] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	digest := sha256.New()
	for _, path := range ordered {
		var document []byte
		if revision == "" {
			document, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if os.IsNotExist(err) {
				continue // A tracked deletion is absent from the working source.
			}
		} else {
			document, err = git(root, "show", result.Revision+":"+path)
		}
		if err != nil {
			return result, fmt.Errorf("read %s: %w", path, err)
		}
		item, err := classify(path, document)
		if err != nil {
			return result, err
		}
		result.Sources = append(result.Sources, item)
		fmt.Fprintf(digest, "%s\x00%s\n", path, item.SHA256)
		categories := result.Go
		if item.Language == "Python" {
			categories = result.Python
		}
		for _, category := range []string{"total", item.Category} {
			value := categories[category]
			value.Files++
			value.Lines += item.Lines
			categories[category] = value
		}
	}
	if len(result.Sources) == 0 {
		return result, fmt.Errorf("no Go or Python sources selected")
	}
	result.SourceDigest = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

func classify(path string, document []byte) (source, error) {
	sum := sha256.Sum256(document)
	item := source{Path: path, Lines: bytes.Count(document, []byte{'\n'}), SHA256: hex.EncodeToString(sum[:])}
	if len(document) != 0 && document[len(document)-1] != '\n' {
		item.Lines++
	}
	if strings.HasSuffix(path, ".py") {
		item.Language, item.Category = "Python", "support"
		if strings.HasPrefix(filepath.Base(path), "test_") {
			item.Category = "test"
		}
		return item, nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, document, parser.ParseComments)
	if err != nil {
		return source{}, fmt.Errorf("parse %s: %w", path, err)
	}
	item.Language, item.Generated = "Go", ast.IsGenerated(file)
	switch {
	case strings.HasSuffix(path, "_test.go"):
		item.Category = "test"
	case item.Generated:
		item.Category = "generated"
	case strings.HasPrefix(path, "conformance/"):
		item.Category = "conformance_support"
	case strings.HasPrefix(path, "examples/"):
		item.Category = "examples"
	default:
		item.Category = "framework_cli_generator_support"
	}
	return item, nil
}
