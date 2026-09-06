//go:build darwin || linux

package compiletest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/fixture"
	"github.com/progresshans/godj/internal/projectgenerate"
)

const (
	// The historical v2 facade supplies a real incompatible generation. Its
	// relevant contract is the source binding rejected by the current bundle.
	checkedInRelationFacadeV2Path = "internal/compiletest/testdata/relation_facade/checked_in_project_facade_v2.go.txt"
	relationFacadeGeneratedPath   = "project/zz_godj_relation_facade.go"
)

func TestCheckedInRelationFacadeV2CannotHybridizeCurrentBundle(t *testing.T) {
	repository := repositoryRoot(t)
	v2Path := filepath.Join(repository, filepath.FromSlash(checkedInRelationFacadeV2Path))
	v2Source, err := os.ReadFile(v2Path)
	if err != nil {
		t.Fatalf("read checked-in relation facade v2 fixture: %v", err)
	}
	v2Digest := sha256.Sum256(v2Source)
	if !bytes.Contains(v2Source, []byte(`GoDjProjectRelationFacadeGeneratorVersion = "godj-codegen-rel-facade-project-current-v2"`)) ||
		bytes.Contains(v2Source, []byte(codegen.ProjectRelationFacadeGeneratorVersion)) {
		t.Fatal("checked-in relation facade v2 fixture has the wrong generation identity")
	}

	spec, err := fixture.ProjectSpec(context.Background())
	if err != nil {
		t.Fatalf("load current relation facade ProjectSpec: %v", err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatalf("generate current relation facade bundle: %v", err)
	}
	currentFacade := requireRelationFacadeGeneratedFile(t, bundle, relationFacadeGeneratedPath)
	versionDeclaration := "GoDjProjectRelationFacadeGeneratorVersion = " + strconv.Quote(codegen.ProjectRelationFacadeGeneratorVersion)
	if !bytes.Contains(currentFacade.Source(), []byte(versionDeclaration)) ||
		bytes.Equal(currentFacade.Source(), v2Source) {
		t.Fatal("current generated relation facade is not an independent current member")
	}

	currentRoot := t.TempDir()
	copyCurrentRelationFacadeProduct(t, currentRoot)
	currentReport, err := projectgenerate.Check(context.Background(), currentRoot, bundle)
	if err != nil || !currentReport.Clean() {
		t.Fatalf("check current relation facade bundle: report=%#v error=%v", currentReport, err)
	}
	currentCompile := compileRelationFacadeProduct(t, repository, filepath.Join(t.TempDir(), "current.test"))
	if currentCompile.err != nil {
		t.Fatalf("compile current relation facade bundle: %v\n%s", currentCompile.err, currentCompile.output)
	}

	staleFacadePath := filepath.Join(currentRoot, filepath.FromSlash(relationFacadeGeneratedPath))
	if err := os.WriteFile(staleFacadePath, v2Source, 0o644); err != nil {
		t.Fatalf("install checked-in relation facade v2 fixture into mixed tree: %v", err)
	}
	staleReport, err := projectgenerate.Check(context.Background(), currentRoot, bundle)
	if !errors.Is(err, projectgenerate.ErrGeneratedDrift) || staleReport.Clean() || staleReport.Interrupted {
		t.Fatalf("check stale/current mixed relation facade bundle: report=%#v error=%v", staleReport, err)
	}
	if len(staleReport.Drifts) != 1 {
		t.Fatalf("stale/current mixed relation facade drifts = %#v, want one modified facade", staleReport.Drifts)
	}
	drift := staleReport.Drifts[0]
	if drift.Path != relationFacadeGeneratedPath || drift.Kind != projectgenerate.DriftModified ||
		drift.ExpectedSHA256 != currentFacade.SHA256 || drift.ActualSHA256 != hex.EncodeToString(v2Digest[:]) {
		t.Fatalf("stale/current mixed relation facade drift = %#v, want exact current/stale binding", drift)
	}

	mixedCompile := compileRelationFacadeProductOverlay(t, repository, v2Path)
	if mixedCompile.err == nil {
		t.Fatal("v2 relation facade unexpectedly compiled with the current product generation")
	}
	for _, fragment := range []string{
		"checked_in_project_facade_v2.go.txt",
		"undefined: goDjProjectSnapshot_",
	} {
		if !strings.Contains(mixedCompile.output, fragment) {
			t.Fatalf("stale/current mixed compile diagnostics lack %q:\n%s", fragment, mixedCompile.output)
		}
	}
}

func requireRelationFacadeGeneratedFile(t *testing.T, bundle codegen.GeneratedBundle, path string) codegen.GeneratedFile {
	t.Helper()
	for _, file := range bundle.Files() {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("current relation facade bundle lacks %s", path)
	return codegen.GeneratedFile{}
}

func copyCurrentRelationFacadeProduct(t *testing.T, destination string) {
	t.Helper()
	fixtureRoot := filepath.Join(repositoryRoot(t), "conformance", "relationdeleteproduct")
	inventory := readRelationFacadeInventory(t, fixtureRoot)
	for _, name := range inventory.names {
		writeProjectBundleCompileFile(t, destination, name, inventory.files[name])
	}
}

func compileRelationFacadeProductOverlay(t *testing.T, repository, facadeBacking string) compileResult {
	t.Helper()
	workspace := t.TempDir()
	overlay, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{
		filepath.Join(repository, "conformance", "relationdeleteproduct", filepath.FromSlash(relationFacadeGeneratedPath)): facadeBacking,
	}})
	if err != nil {
		t.Fatalf("encode stale/current relation facade compile overlay: %v", err)
	}
	overlayPath := filepath.Join(workspace, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		t.Fatalf("write stale/current relation facade compile overlay: %v", err)
	}
	command := exec.CommandContext(
		t.Context(),
		"go",
		"test",
		"-c",
		"-vet=off",
		"-buildvcs=false",
		"-mod=readonly",
		"-overlay="+overlayPath,
		"-o",
		filepath.Join(workspace, "mixed-stale-current.test"),
		modulePath+"/conformance/relationdeleteproduct",
	)
	command.Dir = repository
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}
