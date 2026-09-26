//go:build darwin || linux

package projectgenerate

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/progresshans/godj/codegen"
)

const (
	maxSourceNamespaceFiles          = 4096
	maxSourceNamespaceFileBytes      = 1 << 20
	maxSourceNamespaceAggregateBytes = 64 << 20
	sourceNamespaceFingerprintDomain = "godj/project-source-namespace/v2\x00"
)

type sourceNamespacePlan struct {
	apps []sourceNamespaceApp
}

type sourceNamespaceApp struct {
	directory   string
	packageName string
	importPath  string
	models      map[string]map[string]struct{}
	external    bool
	markers     [4]string
}

type sourceNamespaceModel struct {
	appImportPath string
	rawType       string
	surface       string
}

type sourceNamespaceFile struct {
	path         string
	mode         fs.FileMode
	source       []byte
	readOnlyPath string
}

type sourceNamespaceSnapshot struct {
	sha256        string
	readOnlyPaths []string
}

type sourceNamespaceBudget struct {
	entries        int
	pathBytes      int
	files          int
	aggregateBytes int64
}

func (budget *sourceNamespaceBudget) consumeEntry(relative string) error {
	budget.entries++
	budget.pathBytes += len(relative)
	if budget.entries > maxProjectTreeEntries || budget.pathBytes > maxProjectTreePathBytes {
		return fmt.Errorf(
			"%w: app source inventory exceeds entry or path-byte resource limit at %q",
			ErrGeneratedConflict,
			relative,
		)
	}
	return nil
}

func (budget *sourceNamespaceBudget) consumeSource(size int) error {
	budget.files++
	if budget.files > maxSourceNamespaceFiles {
		return fmt.Errorf("%w: app source inventory exceeds %d files", ErrGeneratedConflict, maxSourceNamespaceFiles)
	}
	budget.aggregateBytes += int64(size)
	if budget.aggregateBytes > maxSourceNamespaceAggregateBytes {
		return fmt.Errorf("%w: app source inventory exceeds %d bytes", ErrGeneratedConflict, maxSourceNamespaceAggregateBytes)
	}
	return nil
}

func sourceNamespacePlanFromBundle(bundle codegen.GeneratedBundle, manifest committedManifest) (sourceNamespacePlan, error) {
	facadePath := joinManifestPath(manifest.Project.Directory, "zz_godj_relation_facade.go")
	var facade []byte
	for _, file := range bundle.Files() {
		if file.Path == facadePath {
			facade = file.Source()
			break
		}
	}
	if len(facade) == 0 {
		return sourceNamespacePlan{}, sourceNamespacePlanInvalid("current project relation facade is missing")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), facadePath, facade, parser.SkipObjectResolution)
	if err != nil {
		return sourceNamespacePlan{}, sourceNamespacePlanInvalid("parse current project relation facade: %v", err)
	}
	if parsed.Name == nil || parsed.Name.Name != manifest.Project.PackageName {
		return sourceNamespacePlan{}, sourceNamespacePlanInvalid("project relation facade package does not match manifest")
	}

	appsByImport := make(map[string]manifestApp, len(manifest.Apps))
	for _, app := range manifest.Apps {
		appsByImport[app.Package.ImportPath] = app
	}
	imports := make(map[string]string)
	for _, imported := range parsed.Imports {
		importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			return sourceNamespacePlan{}, sourceNamespacePlanInvalid("decode facade import: %v", unquoteErr)
		}
		name := ""
		if imported.Name != nil {
			name = imported.Name.Name
		} else if app, ok := appsByImport[importPath]; ok {
			name = app.Package.PackageName
		}
		if name == "" || name == "_" || name == "." {
			continue
		}
		if previous, duplicate := imports[name]; duplicate && previous != importPath {
			return sourceNamespacePlan{}, sourceNamespacePlanInvalid("facade import alias %q is ambiguous", name)
		}
		imports[name] = importPath
	}

	aliases := make(map[string]sourceNamespaceModel)
	structs := make(map[string]*ast.StructType)
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, rawSpec := range general.Specs {
			spec, ok := rawSpec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if structure, ok := spec.Type.(*ast.StructType); ok {
				structs[spec.Name.Name] = structure
			}
			if !spec.Assign.IsValid() {
				continue
			}
			selector, ok := spec.Type.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			packageAlias, ok := selector.X.(*ast.Ident)
			if !ok {
				continue
			}
			importPath, ok := imports[packageAlias.Name]
			if !ok {
				continue
			}
			if _, app := appsByImport[importPath]; !app {
				continue
			}
			if ast.IsExported(spec.Name.Name) || !token.IsIdentifier(spec.Name.Name) || !ast.IsExported(selector.Sel.Name) {
				return sourceNamespacePlan{}, sourceNamespacePlanInvalid("raw-model alias %q has an invalid current shape", spec.Name.Name)
			}
			if _, duplicate := aliases[spec.Name.Name]; duplicate {
				return sourceNamespacePlan{}, sourceNamespacePlanInvalid("raw-model alias %q is duplicated", spec.Name.Name)
			}
			aliases[spec.Name.Name] = sourceNamespaceModel{appImportPath: importPath, rawType: selector.Sel.Name}
		}
	}
	if len(aliases) == 0 {
		if len(manifest.Apps) == 0 {
			return sourceNamespacePlan{}, nil
		}
		return sourceNamespacePlan{}, sourceNamespacePlanInvalid("project relation facade has no private raw-model aliases")
	}

	modelsBySurface := make(map[string]sourceNamespaceModel, len(aliases))
	aliasSurfaces := make(map[string]string, len(aliases))
	for surface, structure := range structs {
		for _, field := range structure.Fields.List {
			if len(field.Names) != 0 || field.Tag != nil {
				continue
			}
			alias, ok := field.Type.(*ast.Ident)
			if !ok {
				continue
			}
			model, rawAlias := aliases[alias.Name]
			if !rawAlias {
				continue
			}
			if !ast.IsExported(surface) {
				return sourceNamespacePlan{}, sourceNamespacePlanInvalid("raw-model alias %q is embedded by unexported surface %q", alias.Name, surface)
			}
			if previous, duplicate := aliasSurfaces[alias.Name]; duplicate {
				return sourceNamespacePlan{}, sourceNamespacePlanInvalid("raw-model alias %q is embedded by both %q and %q", alias.Name, previous, surface)
			}
			if _, duplicate := modelsBySurface[surface]; duplicate {
				return sourceNamespacePlan{}, sourceNamespacePlanInvalid("surface %q embeds multiple raw-model aliases", surface)
			}
			model.surface = surface
			aliasSurfaces[alias.Name] = surface
			modelsBySurface[surface] = model
		}
	}
	if len(aliasSurfaces) != len(aliases) {
		return sourceNamespacePlan{}, sourceNamespacePlanInvalid("not every private raw-model alias is embedded exactly once")
	}

	reservedBySurface := make(map[string]map[string]struct{}, len(modelsBySurface))
	for surface := range modelsBySurface {
		reservedBySurface[surface] = make(map[string]struct{})
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || len(function.Recv.List) != 1 || function.Name == nil || !ast.IsExported(function.Name.Name) {
			continue
		}
		receiver, ok := sourceNamespaceReceiverName(function.Recv.List[0].Type)
		if !ok {
			continue
		}
		methods, wrapper := reservedBySurface[receiver]
		if !wrapper {
			continue
		}
		if _, duplicate := methods[function.Name.Name]; duplicate {
			return sourceNamespacePlan{}, sourceNamespacePlanInvalid("surface %q repeats method %q", receiver, function.Name.Name)
		}
		methods[function.Name.Name] = struct{}{}
	}

	planByImport := make(map[string]*sourceNamespaceApp, len(manifest.Apps))
	for _, app := range manifest.Apps {
		planned := sourceNamespaceApp{
			directory:   app.Package.Directory,
			packageName: app.Package.PackageName,
			importPath:  app.Package.ImportPath,
			models:      make(map[string]map[string]struct{}),
			external:    app.External,
			markers:     codegen.AppSnapshotMarkers(app.SchemaSHA256),
		}
		planByImport[app.Package.ImportPath] = &planned
	}
	for surface, model := range modelsBySurface {
		methods := reservedBySurface[surface]
		for _, required := range []string{"MarshalJSON", "Save", "UnmarshalJSON", "Unwrap"} {
			if _, ok := methods[required]; !ok {
				return sourceNamespacePlan{}, sourceNamespacePlanInvalid("surface %q is missing reserved method %q", surface, required)
			}
		}
		app := planByImport[model.appImportPath]
		if app == nil {
			return sourceNamespacePlan{}, sourceNamespacePlanInvalid("raw model %s has no declared app", model.rawType)
		}
		if _, duplicate := app.models[model.rawType]; duplicate {
			return sourceNamespacePlan{}, sourceNamespacePlanInvalid("raw model %s.%s is repeated", app.importPath, model.rawType)
		}
		app.models[model.rawType] = methods
	}

	plan := sourceNamespacePlan{apps: make([]sourceNamespaceApp, 0, len(manifest.Apps))}
	for _, app := range manifest.Apps {
		planned := planByImport[app.Package.ImportPath]
		if planned == nil || len(planned.models) == 0 {
			return sourceNamespacePlan{}, sourceNamespacePlanInvalid("declared app %q has no raw-model facade surface", app.AppLabel)
		}
		plan.apps = append(plan.apps, *planned)
	}
	sort.Slice(plan.apps, func(left, right int) bool {
		if plan.apps[left].directory != plan.apps[right].directory {
			return plan.apps[left].directory < plan.apps[right].directory
		}
		return plan.apps[left].importPath < plan.apps[right].importPath
	})
	return plan, nil
}

func sourceNamespacePlanInvalid(format string, arguments ...any) error {
	return fmt.Errorf("%w: source namespace plan: %s", ErrInvalidGeneratedBundle, fmt.Sprintf(format, arguments...))
}

func sourceNamespaceReceiverName(expression ast.Expr) (string, bool) {
	switch current := expression.(type) {
	case *ast.Ident:
		return current.Name, true
	case *ast.ParenExpr:
		return sourceNamespaceReceiverName(current.X)
	case *ast.StarExpr:
		return sourceNamespaceReceiverName(current.X)
	case *ast.IndexExpr:
		return sourceNamespaceReceiverName(current.X)
	case *ast.IndexListExpr:
		return sourceNamespaceReceiverName(current.X)
	default:
		return "", false
	}
}

func captureSourceNamespaceSnapshot(
	ctx context.Context,
	projectRoot string,
	manifest committedManifest,
	plan sourceNamespacePlan,
) (sourceNamespaceSnapshot, error) {
	if ctx == nil {
		return sourceNamespaceSnapshot{}, fmt.Errorf("capture source namespace: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return sourceNamespaceSnapshot{}, err
	}
	owned := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		owned[file.Path] = struct{}{}
	}
	if prior, exists, err := readPriorManifest(projectRoot); err == nil && exists {
		for _, file := range prior.Files {
			owned[file.Path] = struct{}{}
		}
	}

	ownedFolded := make(map[string]struct{}, len(owned))
	for relative := range owned {
		ownedFolded[strings.ToLower(relative)] = struct{}{}
	}
	files := make([]sourceNamespaceFile, 0)
	var budget sourceNamespaceBudget
	for _, app := range plan.apps {
		var captured []sourceNamespaceFile
		var err error
		if app.external {
			captured, err = captureExternalAppNamespace(ctx, projectRoot, app, ownedFolded, &budget)
		} else {
			captured, err = captureAppNamespace(ctx, projectRoot, app, owned, &budget, "")
		}
		if err != nil {
			return sourceNamespaceSnapshot{}, err
		}
		files = append(files, captured...)
	}

	sort.Slice(files, func(left, right int) bool { return files[left].path < files[right].path })
	snapshot := sourceNamespaceSnapshot{sha256: sourceNamespaceFingerprint(files)}
	for _, file := range files {
		if file.readOnlyPath != "" {
			snapshot.readOnlyPaths = append(snapshot.readOnlyPaths, file.readOnlyPath)
		}
	}
	return snapshot, nil
}

func verifySourceNamespaceSnapshot(
	ctx context.Context,
	projectRoot string,
	manifest committedManifest,
	plan sourceNamespacePlan,
	want sourceNamespaceSnapshot,
) error {
	current, err := captureSourceNamespaceSnapshot(ctx, projectRoot, manifest, plan)
	if err != nil {
		return err
	}
	if current.sha256 != want.sha256 {
		return fmt.Errorf("%w: non-generated app source changed during generated-boundary verification", ErrGeneratedConflict)
	}
	return nil
}

func sourceNamespaceFingerprint(files []sourceNamespaceFile) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(sourceNamespaceFingerprintDomain))
	writeSourceNamespaceUint64(digest, uint64(len(files)))
	for _, file := range files {
		writeSourceNamespaceUint64(digest, uint64(len(file.path)))
		_, _ = digest.Write([]byte(file.path))
		writeSourceNamespaceUint64(digest, uint64(len(file.readOnlyPath)))
		_, _ = digest.Write([]byte(file.readOnlyPath))
		writeSourceNamespaceUint64(digest, uint64(file.mode.Perm()))
		writeSourceNamespaceUint64(digest, uint64(len(file.source)))
		_, _ = digest.Write(file.source)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

type sourceNamespaceHashWriter interface {
	Write([]byte) (int, error)
}

func writeSourceNamespaceUint64(writer sourceNamespaceHashWriter, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func captureAppNamespace(ctx context.Context, sourceRoot string, app sourceNamespaceApp, owned map[string]struct{}, budget *sourceNamespaceBudget, prefix string) ([]sourceNamespaceFile, error) {
	var files []sourceNamespaceFile
	seenGenerated := make(map[string]bool)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := projectRelativeDirectoryEntries(sourceRoot, app.directory)
	if errors.Is(err, errProjectPathMissing) {
		if app.external {
			return nil, fmt.Errorf("%w: external app directory disappeared", ErrGeneratedConflict)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: inspect app source directory %q: %v", ErrGeneratedConflict, app.directory, err)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		relative := joinManifestPath(app.directory, name)
		displayPath := prefix + relative
		if err := budget.consumeEntry(displayPath); err != nil {
			return nil, err
		}
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, generated := owned[relative]; !app.external && generated {
			continue
		}
		if strings.HasPrefix(name, "zz_godj_") && (!app.external || !externalGeneratedFilename(name)) {
			return nil, fmt.Errorf("%w: unowned generated source %q in app namespace", ErrGeneratedConflict, relative)
		}
		maximum := int64(maxSourceNamespaceFileBytes)
		if app.external && externalGeneratedFilename(name) {
			maximum = maxSourceNamespaceAggregateBytes
		}
		contents, mode, err := readRegularProjectFileBounded(sourceRoot, relative, maximum)
		if err != nil {
			return nil, fmt.Errorf("%w: read app source %q: %v", ErrGeneratedConflict, relative, err)
		}
		if err := budget.consumeSource(len(contents)); err != nil {
			return nil, err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), relative, contents, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("%w: parse app source %q: %v", ErrGeneratedConflict, relative, err)
		}
		if parsed.Name == nil || parsed.Name.Name != app.packageName {
			return nil, fmt.Errorf("%w: app source %q declares package %q, want %q", ErrGeneratedConflict, relative, parsed.Name.Name, app.packageName)
		}
		if app.external && externalGeneratedFilename(name) {
			if err := requireExternalAppMarker(parsed, name, app.markers); err != nil {
				return nil, err
			}
			seenGenerated[name] = true
		}
		for _, declaration := range parsed.Decls {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) != 1 || function.Name == nil || !ast.IsExported(function.Name.Name) {
				continue
			}
			receiver, ok := sourceNamespaceReceiverName(function.Recv.List[0].Type)
			if !ok {
				continue
			}
			reserved, rawModel := app.models[receiver]
			if !rawModel {
				continue
			}
			if _, collision := reserved[function.Name.Name]; collision {
				return nil, fmt.Errorf(
					"%w: app source %q declares reserved generated method %s.%s.%s",
					ErrGeneratedConflict, relative, app.importPath, receiver, function.Name.Name,
				)
			}
		}
		files = append(files, sourceNamespaceFile{path: displayPath, mode: mode, source: contents})
	}
	if app.external {
		for _, name := range currentAppFilenames {
			if !seenGenerated[name] {
				return nil, fmt.Errorf("%w: external app %q is missing generated companion %q", ErrGeneratedConflict, app.importPath, name)
			}
		}
	}
	return files, nil
}
