//go:build darwin || linux

package projectgenerate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func externalGeneratedFilename(name string) bool {
	for _, current := range currentAppFilenames {
		if name == current {
			return true
		}
	}
	return false
}

func requireExternalAppMarker(file *ast.File, name string, markers [4]string) error {
	part := -1
	for index, filename := range currentAppFilenames {
		if name == filename {
			part = index
			break
		}
	}
	if part < 0 {
		return fmt.Errorf("%w: unknown external app companion", ErrGeneratedConflict)
	}
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}
		for _, entry := range group.Specs {
			spec, ok := entry.(*ast.TypeSpec)
			if !ok || spec.Name.Name != markers[part] || spec.Assign.IsValid() {
				continue
			}
			shape, ok := spec.Type.(*ast.StructType)
			if ok && shape.Fields != nil && len(shape.Fields.List) == 0 {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: external app companion %q has a different schema or generation ABI", ErrGeneratedConflict, name)
}

// A dependency remains outside the publisher's write domain. Resolve and read
// it with the same isolated module/toolchain policy as candidate compilation;
// its bytes still participate in before/after namespace ownership checks.
func captureExternalAppNamespace(ctx context.Context, projectRoot string, app sourceNamespaceApp, ownedFolded map[string]struct{}, budget *sourceNamespaceBudget) (result []sourceNamespaceFile, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspace, err := createCandidateWorkspace(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: prepare external app inspection: %w", ErrGeneratedConflict, err)
	}
	defer func() {
		if err := os.RemoveAll(workspace.root); err != nil {
			result = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("%w: remove external app inspection workspace: %w", ErrGeneratedConflict, err))
		}
	}()
	output, _, err := runCandidateStructuredCommand(ctx, projectRoot, workspace.environment, maxCandidateListBytes,
		"go", "list", "-mod=readonly", "-find", "-json", app.importPath)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve external app %q: %w", ErrGeneratedConflict, app.importPath, err)
	}
	var resolved struct {
		ImportPath string
		Dir        string
		Name       string
		Error      json.RawMessage
	}
	if err := json.Unmarshal([]byte(output), &resolved); err != nil || resolved.ImportPath != app.importPath || resolved.Dir == "" ||
		!filepath.IsAbs(resolved.Dir) || resolved.Name != app.packageName || (len(resolved.Error) != 0 && string(resolved.Error) != "null") {
		return nil, fmt.Errorf("%w: external app resolution is incomplete or mismatched", ErrGeneratedConflict)
	}
	root, err := canonicalProjectRoot(resolved.Dir)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect external app directory: %w", ErrGeneratedConflict, err)
	}
	app.directory = "."
	prefix := "external:" + strings.TrimSuffix(app.importPath, "/") + "/"
	result, err = captureAppNamespace(ctx, root, app, nil, budget, prefix)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(projectRoot, root)
	if err != nil {
		return nil, fmt.Errorf("%w: locate external app relative to publication root: %w", ErrGeneratedConflict, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		// Match the manifest's portable case-folded ownership policy, even
		// when a dependency reaches the same root through a case alias.
		folded, err := filepath.Rel(strings.ToLower(projectRoot), strings.ToLower(root))
		if err != nil || folded != ".." && !strings.HasPrefix(folded, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: external app case-aliases the publication root", ErrGeneratedConflict)
		}
		return result, nil
	}
	relative = filepath.ToSlash(relative)
	folded := strings.ToLower(relative)
	if folded == ".godj" || strings.HasPrefix(folded, ".godj/") {
		return nil, fmt.Errorf("%w: external app overlaps publication control directory", ErrGeneratedConflict)
	}
	for index := range result {
		name := strings.TrimPrefix(result[index].path, prefix)
		if !externalGeneratedFilename(name) {
			continue
		}
		local := joinManifestPath(relative, name)
		if _, writable := ownedFolded[strings.ToLower(local)]; writable {
			return nil, fmt.Errorf("%w: external app file %q overlaps current or prior publication ownership", ErrGeneratedConflict, local)
		}
		result[index].readOnlyPath = local
	}
	return result, nil
}
