package compiletest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const modulePath = "github.com/progresshans/godj"

const (
	relationFacadePhysicalBytes   = 62538
	relationFacadePhysicalDigest  = "992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb"
	relationFacadeGeneratedBytes  = 26140
	relationFacadeGeneratedDigest = "a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628"
	relationFacadeLogicalBytes    = 65970
	relationFacadeLogicalDigest   = "29d37c4cc1446ce320bcd5476afafb77989cd980a1dd3f96cb0732803835737f"
)

var relationFacadePhysicalFiles = []string{
	"authors/zz_godj_generated.go",
	"authors/zz_godj_relation.go",
	"authors/zz_godj_relation_object.go",
	"authors/zz_godj_relation_projection.go",
	"blog/zz_godj_generated.go",
	"blog/zz_godj_relation.go",
	"blog/zz_godj_relation_object.go",
	"blog/zz_godj_relation_projection.go",
	"blog/zz_godj_relation_query.go",
	"fixture/schema.go",
	"observer.go",
	"product_test.go",
	"project/zz_godj_bindings.go",
	"project/zz_godj_relation_delete.go",
	"project/zz_godj_relation_object.go",
	"project/zz_godj_relation_select_related.go",
}

var relationFacadePhysicalDirectories = []string{
	".",
	"authors",
	"blog",
	"fixture",
	"project",
}

var relationFacadeGeneratedFiles = []string{
	"authors/zz_godj_generated.go",
	"authors/zz_godj_relation.go",
	"authors/zz_godj_relation_object.go",
	"authors/zz_godj_relation_projection.go",
	"blog/zz_godj_generated.go",
	"blog/zz_godj_relation.go",
	"blog/zz_godj_relation_object.go",
	"blog/zz_godj_relation_projection.go",
	"blog/zz_godj_relation_query.go",
	"project/zz_godj_bindings.go",
	"project/zz_godj_relation_delete.go",
	"project/zz_godj_relation_object.go",
	"project/zz_godj_relation_select_related.go",
}

func TestExternalConsumerCompiles(t *testing.T) {
	for _, fixture := range []string{
		"external_consumer.go.txt",
		"write_external_consumer.go.txt",
		"save_external_consumer.go.txt",
		"migration_external_consumer.go.txt",
		"migration_definition_external_consumer.go.txt",
		"project_external_consumer.go.txt",
		"relation_project/external_consumer.go.txt",
		"relation_query/external_consumer.go.txt",
		"relation_object/external_consumer.go.txt",
		"relation_reverse/external_consumer.go.txt",
		"relation_prefetch/external_consumer.go.txt",
		"relation_select_related/external_consumer.go.txt",
		"relation_delete/backend_external_consumer.go.txt",
		"relation_delete/generated_external_consumer.go.txt",
	} {
		result := compileFixture(t, fixture)
		if result.err != nil {
			t.Fatalf("external consumer %s did not compile: %v\n%s", fixture, result.err, result.output)
		}
	}

	verifyRelationFacadeCompileSpike(t)
}

func verifyRelationFacadeCompileSpike(t *testing.T) {
	t.Helper()

	root, err := filepath.EvalSymlinks(repositoryRoot(t))
	if err != nil {
		t.Fatalf("canonicalize repository root: %v", err)
	}
	testdataRoot, err := canonicalRelationFacadeTestdataRoot(root)
	if err != nil {
		t.Fatalf("validate relation facade testdata root: %v", err)
	}
	overlayBacking, err := canonicalRelationFacadeFixture(testdataRoot, "project_facade_spike.go.txt")
	if err != nil {
		t.Fatalf("validate relation facade overlay backing: %v", err)
	}
	consumerBacking, err := canonicalRelationFacadeFixture(testdataRoot, "external_consumer.go.txt")
	if err != nil {
		t.Fatalf("validate relation facade external consumer backing: %v", err)
	}
	overlaySource, err := os.ReadFile(overlayBacking)
	if err != nil {
		t.Fatalf("read relation facade overlay backing: %v", err)
	}
	consumerSource, err := os.ReadFile(consumerBacking)
	if err != nil {
		t.Fatalf("read relation facade external consumer: %v", err)
	}
	if err := validateRelationFacadeOverlaySource(overlaySource); err != nil {
		t.Fatalf("validate relation facade overlay source: %v", err)
	}
	if err := validateRelationFacadeConsumerSource(consumerSource); err != nil {
		t.Fatalf("validate relation facade consumer source: %v", err)
	}
	directFirstMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("post, found, err := models.BlogPosts.\n\t\tOrderBy(blog.PostFields.ID.Asc()).\n\t\tFirst(ctx)"),
		[]byte("post, found, err := models.BlogPosts.First(ctx)\n\t_ = models.BlogPosts.OrderBy(blog.PostFields.ID.Asc())"),
	))
	if err := validateRelationFacadeConsumerSource(directFirstMutation); err == nil || !strings.Contains(err.Error(), "direct ordered First assignments") {
		t.Fatalf("direct ordered First mutation error = %v, want exact-chain rejection", err)
	}
	mainOriginMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("models, err := project.Using(backend)"),
		[]byte("models, err := project.Using(nil)"),
	))
	if err := validateRelationFacadeConsumerSource(mainOriginMutation); err == nil || !strings.Contains(err.Error(), "main consumer project.Using(backend) assignments") {
		t.Fatalf("main-origin mutation error = %v, want exact backend-origin rejection", err)
	}
	sessionOriginMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("models, err := project.Using(session)"),
		[]byte("models, err := project.Using(nil)"),
	))
	if err := validateRelationFacadeConsumerSource(sessionOriginMutation); err == nil || !strings.Contains(err.Error(), "session callback project.Using(session) assignments") {
		t.Fatalf("session-origin mutation error = %v, want exact callback-local session rejection", err)
	}
	sessionGoMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("models, err := project.Using(session)"),
		[]byte("var models project.Models\n\t\tvar err error\n\t\tdone := make(chan struct{})\n\t\tgo func() {\n\t\t\tmodels, err = project.Using(session)\n\t\t\tclose(done)\n\t\t}()\n\t\t<-done"),
	))
	if err := validateRelationFacadeConsumerSource(sessionGoMutation); err == nil || !strings.Contains(err.Error(), "session consumer contains forbidden GoStmt") {
		t.Fatalf("session goroutine mutation error = %v, want synchronous-callback rejection", err)
	}
	sessionDeferMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("models, err := project.Using(session)"),
		[]byte("defer func() {}()\n\t\tmodels, err := project.Using(session)"),
	))
	if err := validateRelationFacadeConsumerSource(sessionDeferMutation); err == nil || !strings.Contains(err.Error(), "session consumer contains forbidden DeferStmt") {
		t.Fatalf("session defer mutation error = %v, want deferred-callback rejection", err)
	}
	sessionNestedLiteralMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("models, err := project.Using(session)"),
		[]byte("_ = func() {}\n\t\tmodels, err := project.Using(session)"),
	))
	if err := validateRelationFacadeConsumerSource(sessionNestedLiteralMutation); err == nil || !strings.Contains(err.Error(), "session consumer function literals") {
		t.Fatalf("session nested-literal mutation error = %v, want exact-one callback literal rejection", err)
	}

	mutatedImport := replaceRelationFacadeToken(t, overlaySource, []byte("\t\"context\"\n"), []byte("\t\"context\"\n\t\"unsafe\"\n"))
	if err := validateRelationFacadeOverlaySource(mutatedImport); err == nil || !strings.Contains(err.Error(), `forbidden overlay import "unsafe"`) {
		t.Fatalf("forbidden-import AST mutation error = %v, want unsafe import rejection", err)
	}
	mutatedSymbol := replaceRelationFacadeToken(t, overlaySource, []byte("BindObjects()"), []byte("BindRelationDeleters()"))
	if err := validateRelationFacadeOverlaySource(mutatedSymbol); err == nil || !strings.Contains(err.Error(), `forbidden overlay call "BindRelationDeleters"`) {
		t.Fatalf("forbidden-symbol AST mutation error = %v, want BindRelationDeleters rejection", err)
	}
	relationDeletersBodyMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("objects, err := BindObjects()"),
		[]byte("objects, err := BindObjects()\n\tvar _ RelationDeleters"),
	))
	if err := validateRelationFacadeOverlaySource(relationDeletersBodyMutation); err == nil || !strings.Contains(err.Error(), `forbidden overlay identifier "RelationDeleters"`) {
		t.Fatalf("RelationDeleters body mutation error = %v, want all-position identifier rejection", err)
	}
	reviewerTypeBodyMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("return query.spikeFactory.SelectRelated(query.spikeQuerySet).Author().All(ctx)"),
		[]byte("var _ BlogPostReviewerSelectRelatedQuery\n\treturn query.spikeFactory.SelectRelated(query.spikeQuerySet).Author().All(ctx)"),
	))
	if err := validateRelationFacadeOverlaySource(reviewerTypeBodyMutation); err == nil || !strings.Contains(err.Error(), `forbidden overlay identifier "BlogPostReviewerSelectRelatedQuery"`) {
		t.Fatalf("Reviewer-query type body mutation error = %v, want all-position identifier rejection", err)
	}
	reviewerQueryBodyMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("return query.spikeFactory.SelectRelated(query.spikeQuerySet).Author().All(ctx)"),
		[]byte("return query.spikeFactory.SelectRelated(query.spikeQuerySet).Reviewer().All(ctx)"),
	))
	if err := validateRelationFacadeOverlaySource(reviewerQueryBodyMutation); err == nil || !strings.Contains(err.Error(), `forbidden overlay selector "Reviewer"`) {
		t.Fatalf("Reviewer-query call body mutation error = %v, want query-path rejection", err)
	}
	shadowedBinderMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("objects, err := BindObjects()"),
		[]byte("BindObjects := BindObjects\n\tobjects, err := BindObjects()"),
	))
	if err := validateRelationFacadeOverlaySource(shadowedBinderMutation); err == nil || !strings.Contains(err.Error(), `forbidden overlay local shadow "BindObjects"`) {
		t.Fatalf("shadowed BindObjects mutation error = %v, want local-binding rejection", err)
	}
	shadowedBlogMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("\treturn Models{\n"),
		[]byte("\t{\n\t\tblog := blog.PostObjects\n\t\t_ = blog\n\t}\n\treturn Models{\n"),
	))
	if err := validateRelationFacadeOverlaySource(shadowedBlogMutation); err == nil || !strings.Contains(err.Error(), `forbidden overlay local shadow "blog"`) {
		t.Fatalf("shadowed blog mutation error = %v, want import-qualifier binding rejection", err)
	}
	overlayLiteralMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("objects, err := BindObjects()"),
		[]byte("_ = func() {}\n\tobjects, err := BindObjects()"),
	))
	if err := validateRelationFacadeOverlaySource(overlayLiteralMutation); err == nil || !strings.Contains(err.Error(), "forbidden overlay function literal") {
		t.Fatalf("overlay function-literal mutation error = %v, want literal rejection", err)
	}
	deleteVersionMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("objects, err := BindObjects()"),
		[]byte("objects, err := BindObjects()\n\t_ = GoDjProjectRelationDeleteGeneratorVersion"),
	))
	if err := validateRelationFacadeOverlaySource(deleteVersionMutation); err == nil || !strings.Contains(err.Error(), `forbidden overlay identifier "GoDjProjectRelationDeleteGeneratorVersion"`) {
		t.Fatalf("delete generator-version mutation error = %v, want product-claim symbol rejection", err)
	}
	relationDeletersMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("type relationFacadeAuthorToken struct{}"),
		[]byte("type relationFacadeAuthorToken RelationDeleters"),
	))
	if err := validateRelationFacadeOverlaySource(relationDeletersMutation); err == nil || !strings.Contains(err.Error(), "overlay type schema relationFacadeAuthorToken") {
		t.Fatalf("RelationDeleters type mutation error = %v, want exact type-schema rejection", err)
	}
	reviewerMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("type BlogPostRelationFacadeSelectors struct {\n\tAuthor relationFacadeAuthorToken\n}"),
		[]byte("type BlogPostRelationFacadeSelectors struct {\n\tAuthor relationFacadeAuthorToken\n\tReviewer relationFacadeAuthorToken\n}"),
	))
	if err := validateRelationFacadeOverlaySource(reviewerMutation); err == nil || !strings.Contains(err.Error(), "overlay type schema BlogPostRelationFacadeSelectors") {
		t.Fatalf("reviewer selector mutation error = %v, want exact type-schema rejection", err)
	}
	privateEmbeddedMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("type BlogPostFacadeQuery struct {\n\tRelated"),
		[]byte("type BlogPostFacadeQuery struct {\n\tBlogPostObjectFactory\n\tRelated"),
	))
	if err := validateRelationFacadeOverlaySource(privateEmbeddedMutation); err == nil || !strings.Contains(err.Error(), "overlay type schema BlogPostFacadeQuery") {
		t.Fatalf("private embedded-kernel mutation error = %v, want exact type-schema rejection", err)
	}
	signatureMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("func Using(backend db.Queryer) (Models, error)"),
		[]byte("func Using(backend any) (Models, error)"),
	))
	if err := validateRelationFacadeOverlaySource(signatureMutation); err == nil || !strings.Contains(err.Error(), "overlay function signature .Using") {
		t.Fatalf("Using signature mutation error = %v, want exact signature rejection", err)
	}
	receiverMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		overlaySource,
		[]byte("func (query BlogPostFacadeQuery) Filter"),
		[]byte("func (query *BlogPostFacadeQuery) Filter"),
	))
	if err := validateRelationFacadeOverlaySource(receiverMutation); err == nil || !strings.Contains(err.Error(), "overlay receiver schema BlogPostFacadeQuery.Filter") {
		t.Fatalf("pointer-receiver mutation error = %v, want exact receiver rejection", err)
	}
	verifyRelationFacadeInventoryRejections(t)

	fixtureRoot := filepath.Join(root, "conformance", "relationdeleteproduct")
	physicalBefore := readRelationFacadeInventory(t, fixtureRoot)
	verifyRelationFacadePhysicalInventory(t, physicalBefore)

	projectRoot, err := filepath.EvalSymlinks(filepath.Join(fixtureRoot, "project"))
	if err != nil {
		t.Fatalf("canonicalize relation facade project directory: %v", err)
	}
	virtualName := "zz_godj_relation_facade_spike.go"
	virtualTarget := filepath.Join(projectRoot, virtualName)
	requireRelationFacadeTargetAbsent(t, virtualTarget, "before no-overlay compile")

	logical := cloneRelationFacadeFiles(physicalBefore.files)
	logical[filepath.ToSlash(filepath.Join("project", virtualName))] = slices.Clone(overlaySource)
	logicalBytes, logicalDigest := digestRelationFacadeFiles(logical)
	if len(logical) != 17 {
		t.Fatalf("relation facade logical inventory count = %d, want 17", len(logical))
	}
	if logicalBytes != relationFacadeLogicalBytes || logicalDigest != relationFacadeLogicalDigest {
		t.Fatalf("relation facade logical inventory = %d/%s, want %d/%s", logicalBytes, logicalDigest, relationFacadeLogicalBytes, relationFacadeLogicalDigest)
	}

	directory := t.TempDir()
	goMod := fmt.Sprintf(`module example.com/godj-relation-facade-spike

go 1.26.0

require %s v0.0.0

replace %s => %s
`, modulePath, modulePath, filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write relation facade go.mod: %v", err)
	}
	consumerPath := filepath.Join(directory, "consumer.go")
	if err := os.WriteFile(consumerPath, consumerSource, 0o644); err != nil {
		t.Fatalf("write relation facade consumer: %v", err)
	}

	requireRelationFacadeTargetAbsent(t, virtualTarget, "immediately before no-overlay compile")
	negative := compileRelationFacadeConsumer(t, directory, "", "no-overlay.test")
	if negative.err == nil {
		t.Fatal("relation facade candidate unexpectedly compiled without overlay")
	}
	if count := strings.Count(negative.output, "undefined: project.Using"); count != 2 {
		t.Fatalf("no-overlay project.Using diagnostics = %d, want exact 2:\n%s", count, negative.output)
	}
	requireRelationFacadeTargetAbsent(t, virtualTarget, "after no-overlay compile")

	overlayPath := filepath.Join(directory, "overlay.json")
	overlayDocument := struct {
		Replace map[string]string
	}{
		Replace: map[string]string{virtualTarget: overlayBacking},
	}
	overlayBytes, err := json.Marshal(overlayDocument)
	if err != nil {
		t.Fatalf("encode relation facade overlay: %v", err)
	}
	if err := os.WriteFile(overlayPath, overlayBytes, 0o644); err != nil {
		t.Fatalf("write relation facade overlay: %v", err)
	}
	var decodedOverlay struct {
		Replace map[string]string
	}
	if err := json.Unmarshal(overlayBytes, &decodedOverlay); err != nil {
		t.Fatalf("decode relation facade overlay: %v", err)
	}
	if len(decodedOverlay.Replace) != 1 || decodedOverlay.Replace[virtualTarget] != overlayBacking {
		t.Fatalf("relation facade overlay mapping = %#v, want exact virtual-to-backing mapping", decodedOverlay.Replace)
	}

	requireRelationFacadeTargetAbsent(t, virtualTarget, "immediately before overlay compile")
	verifyRelationFacadeOverlayGoList(t, root, overlayPath, virtualName)
	productOutput := filepath.Join(directory, "relationdeleteproduct.test")
	productCompile := compileRelationFacadeProduct(t, root, overlayPath, productOutput)
	if productCompile.err != nil {
		t.Fatalf("relation facade logical exact-17 product did not compile: %v\n%s", productCompile.err, productCompile.output)
	}
	productInfo, err := os.Lstat(productOutput)
	if err != nil {
		t.Fatalf("lstat relation facade compile-only product: %v", err)
	}
	if !productInfo.Mode().IsRegular() {
		t.Fatalf("relation facade compile-only product mode = %s, want regular file", productInfo.Mode())
	}
	requireRelationFacadeTargetAbsent(t, virtualTarget, "after logical exact-17 product compile")
	positive := compileRelationFacadeConsumer(t, directory, overlayPath, "overlay.test")
	if positive.err != nil {
		t.Fatalf("relation facade candidate did not compile with overlay: %v\n%s", positive.err, positive.output)
	}

	predicateMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte(`blog.PostFields.Title.IContains("lazy")`),
		[]byte(`authors.AuthorFields.Name.IContains("lazy")`),
	)
	if err := os.WriteFile(consumerPath, predicateMutation, 0o644); err != nil {
		t.Fatalf("write relation facade predicate mutation: %v", err)
	}
	predicateNegative := compileRelationFacadeConsumer(t, directory, overlayPath, "predicate-negative.test")
	if predicateNegative.err == nil {
		t.Fatal("relation facade wrong-model predicate unexpectedly compiled")
	}
	for _, fragment := range []string{"orm.Predicate[authors.Author]", "orm.Predicate[blog.Post]"} {
		if !strings.Contains(predicateNegative.output, fragment) {
			t.Fatalf("wrong-predicate diagnostics do not contain %q:\n%s", fragment, predicateNegative.output)
		}
	}

	orderingMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("ordered := filtered.OrderBy(blog.PostFields.ID.Asc())"),
		[]byte("ordered := filtered.OrderBy(authors.AuthorFields.ID.Asc())"),
	)
	if err := os.WriteFile(consumerPath, orderingMutation, 0o644); err != nil {
		t.Fatalf("write relation facade ordering mutation: %v", err)
	}
	orderingNegative := compileRelationFacadeConsumer(t, directory, overlayPath, "ordering-negative.test")
	if orderingNegative.err == nil {
		t.Fatal("relation facade wrong-model ordering unexpectedly compiled")
	}
	for _, fragment := range []string{"orm.Ordering[authors.Author]", "orm.Ordering[blog.Post]"} {
		if !strings.Contains(orderingNegative.output, fragment) {
			t.Fatalf("wrong-ordering diagnostics do not contain %q:\n%s", fragment, orderingNegative.output)
		}
	}

	selectorMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("models.BlogPosts.Related.Author"),
		[]byte("blog.PostFields.ID"),
	)
	if err := os.WriteFile(consumerPath, selectorMutation, 0o644); err != nil {
		t.Fatalf("write relation facade selector mutation: %v", err)
	}
	selectorNegative := compileRelationFacadeConsumer(t, directory, overlayPath, "selector-negative.test")
	if selectorNegative.err == nil {
		t.Fatal("relation facade wrong selector token unexpectedly compiled")
	}
	for _, fragment := range []string{"orm.IntegerField[blog.Post]", "project.relationFacadeAuthorToken"} {
		if !strings.Contains(selectorNegative.output, fragment) {
			t.Fatalf("wrong-selector diagnostics do not contain %q:\n%s", fragment, selectorNegative.output)
		}
	}
	if err := os.WriteFile(consumerPath, consumerSource, 0o644); err != nil {
		t.Fatalf("restore relation facade consumer: %v", err)
	}

	requireRelationFacadeTargetAbsent(t, virtualTarget, "after overlay and mutation compiles")
	physicalAfter := readRelationFacadeInventory(t, fixtureRoot)
	verifyRelationFacadePhysicalInventory(t, physicalAfter)
	if !equalRelationFacadeFiles(physicalBefore.files, physicalAfter.files) {
		t.Fatal("relation facade physical fixture bytes changed during overlay compile")
	}
	afterOverlayBacking, err := canonicalRelationFacadeFixture(testdataRoot, "project_facade_spike.go.txt")
	if err != nil {
		t.Fatalf("revalidate relation facade overlay backing: %v", err)
	}
	afterConsumerBacking, err := canonicalRelationFacadeFixture(testdataRoot, "external_consumer.go.txt")
	if err != nil {
		t.Fatalf("revalidate relation facade external consumer backing: %v", err)
	}
	if afterOverlayBacking != overlayBacking || afterConsumerBacking != consumerBacking {
		t.Fatalf("relation facade backing paths changed during compile: %q/%q, want %q/%q", afterOverlayBacking, afterConsumerBacking, overlayBacking, consumerBacking)
	}
}

func canonicalRelationFacadeTestdataRoot(root string) (string, error) {
	expected := filepath.Clean(filepath.Join(root, "internal", "compiletest", "testdata", "relation_facade"))
	info, err := os.Lstat(expected)
	if err != nil {
		return "", fmt.Errorf("lstat %s: %w", expected, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("relation facade testdata root is a symlink: %s", expected)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("relation facade testdata root mode = %s, want directory", info.Mode())
	}
	canonical, err := filepath.EvalSymlinks(expected)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", expected, err)
	}
	if canonical != expected {
		return "", fmt.Errorf("relation facade testdata root canonical path = %s, want exact %s", canonical, expected)
	}
	return canonical, nil
}

func canonicalRelationFacadeFixture(testdataRoot, name string) (string, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("invalid relation facade fixture name %q", name)
	}
	expected := filepath.Clean(filepath.Join(testdataRoot, name))
	info, err := os.Lstat(expected)
	if err != nil {
		return "", fmt.Errorf("lstat %s: %w", expected, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("relation facade fixture is a symlink: %s", expected)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("relation facade fixture mode = %s, want regular file: %s", info.Mode(), expected)
	}
	canonical, err := filepath.EvalSymlinks(expected)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", expected, err)
	}
	relative, err := filepath.Rel(testdataRoot, canonical)
	if err != nil {
		return "", fmt.Errorf("confine relation facade fixture %s: %w", canonical, err)
	}
	if filepath.IsAbs(relative) || relative != name || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("relation facade fixture canonical path %s escapes exact testdata root %s", canonical, testdataRoot)
	}
	if canonical != expected {
		return "", fmt.Errorf("relation facade fixture canonical path = %s, want exact %s", canonical, expected)
	}
	return canonical, nil
}

func compileRelationFacadeConsumer(t *testing.T, directory, overlayPath, outputName string) compileResult {
	t.Helper()

	arguments := []string{"test", "-c", "-mod=mod"}
	if overlayPath != "" {
		arguments = append(arguments, "-overlay="+overlayPath)
	}
	arguments = append(arguments, "-o", filepath.Join(directory, outputName), ".")
	command := exec.CommandContext(t.Context(), "go", arguments...)
	command.Dir = directory
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

func compileRelationFacadeProduct(t *testing.T, root, overlayPath, outputPath string) compileResult {
	t.Helper()

	command := exec.CommandContext(
		t.Context(),
		"go",
		"test",
		"-c",
		"-mod=readonly",
		"-overlay="+overlayPath,
		"-o",
		outputPath,
		modulePath+"/conformance/relationdeleteproduct",
	)
	command.Dir = root
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

type relationFacadeInventory struct {
	files  map[string][]byte
	names  []string
	bytes  int
	digest string
}

func readRelationFacadeInventory(t *testing.T, root string) relationFacadeInventory {
	t.Helper()

	inventory, err := loadRelationFacadeInventory(root, relationFacadePhysicalDirectories, relationFacadePhysicalFiles)
	if err != nil {
		t.Fatalf("read relation facade physical inventory: %v", err)
	}
	return inventory
}

func loadRelationFacadeInventory(root string, wantDirectories, wantFiles []string) (relationFacadeInventory, error) {
	directories := make(map[string]bool, len(wantDirectories))
	for _, name := range wantDirectories {
		directories[name] = false
	}
	files := make(map[string][]byte, len(wantFiles))
	expectedFiles := make(map[string]bool, len(wantFiles))
	for _, name := range wantFiles {
		expectedFiles[name] = false
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("relation facade physical fixture contains symlink %s", path)
		}
		if entry.IsDir() {
			if _, expected := directories[relative]; !expected {
				return fmt.Errorf("relation facade physical fixture contains unexpected directory %s", relative)
			}
			directories[relative] = true
			return nil
		}
		if err := validateRelationFacadeInventoryEntry(path, entry); err != nil {
			return err
		}
		if _, expected := expectedFiles[relative]; !expected {
			return fmt.Errorf("relation facade physical fixture contains unexpected Go entry %s", relative)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = content
		expectedFiles[relative] = true
		return nil
	})
	if err != nil {
		return relationFacadeInventory{}, err
	}
	for name, seen := range directories {
		if !seen {
			return relationFacadeInventory{}, fmt.Errorf("relation facade physical fixture is missing directory %s", name)
		}
	}
	for name, seen := range expectedFiles {
		if !seen {
			return relationFacadeInventory{}, fmt.Errorf("relation facade physical fixture is missing Go entry %s", name)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	contentBytes, digest := digestRelationFacadeFiles(files)
	return relationFacadeInventory{files: files, names: names, bytes: contentBytes, digest: digest}, nil
}

func validateRelationFacadeInventoryEntry(path string, entry fs.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("relation facade physical fixture contains symlink %s", path)
	}
	if entry.IsDir() {
		return nil
	}
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect relation facade physical entry %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("relation facade physical fixture contains non-regular entry %s (%s)", path, info.Mode())
	}
	if filepath.Ext(entry.Name()) != ".go" {
		return fmt.Errorf("relation facade physical fixture contains unexpected non-Go entry %s", path)
	}
	return nil
}

func verifyRelationFacadeInventoryRejections(t *testing.T) {
	t.Helper()

	extraRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extraRoot, "valid.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory Go entry: %v", err)
	}
	wantDirectories := []string{"."}
	wantFiles := []string{"valid.go"}
	if _, err := loadRelationFacadeInventory(extraRoot, wantDirectories, wantFiles); err != nil {
		t.Fatalf("strict inventory rejected regular Go entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extraRoot, "unexpected.txt"), []byte("unexpected\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory extra entry: %v", err)
	}
	if _, err := loadRelationFacadeInventory(extraRoot, wantDirectories, wantFiles); err == nil || !strings.Contains(err.Error(), "unexpected non-Go entry") {
		t.Fatalf("strict inventory extra-entry error = %v, want non-Go rejection", err)
	}

	extraGoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extraGoRoot, "valid.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory expected Go entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extraGoRoot, "extra.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory extra Go entry: %v", err)
	}
	if _, err := loadRelationFacadeInventory(extraGoRoot, wantDirectories, wantFiles); err == nil || !strings.Contains(err.Error(), "unexpected Go entry extra.go") {
		t.Fatalf("strict inventory extra-Go error = %v, want exact-entry rejection", err)
	}

	extraDirectoryRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extraDirectoryRoot, "valid.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write strict-inventory directory fixture: %v", err)
	}
	if err := os.Mkdir(filepath.Join(extraDirectoryRoot, "unexpected"), 0o755); err != nil {
		t.Fatalf("write strict-inventory extra directory: %v", err)
	}
	if _, err := loadRelationFacadeInventory(extraDirectoryRoot, wantDirectories, wantFiles); err == nil || !strings.Contains(err.Error(), "unexpected directory unexpected") {
		t.Fatalf("strict inventory extra-directory error = %v, want exact-directory rejection", err)
	}

	for _, adversary := range []struct {
		name         string
		mode         fs.FileMode
		wantFragment string
	}{
		{name: "linked.go", mode: os.ModeSymlink, wantFragment: "contains symlink"},
		{name: "pipe.go", mode: os.ModeNamedPipe, wantFragment: "non-regular entry"},
	} {
		entry := relationFacadeAdversarialDirEntry{name: adversary.name, mode: adversary.mode}
		if err := validateRelationFacadeInventoryEntry(filepath.Join(extraRoot, adversary.name), entry); err == nil || !strings.Contains(err.Error(), adversary.wantFragment) {
			t.Fatalf("strict inventory %s error = %v, want %q", adversary.name, err, adversary.wantFragment)
		}
	}
}

type relationFacadeAdversarialDirEntry struct {
	name string
	mode fs.FileMode
}

func (entry relationFacadeAdversarialDirEntry) Name() string               { return entry.name }
func (entry relationFacadeAdversarialDirEntry) IsDir() bool                { return entry.mode.IsDir() }
func (entry relationFacadeAdversarialDirEntry) Type() fs.FileMode          { return entry.mode.Type() }
func (entry relationFacadeAdversarialDirEntry) Info() (fs.FileInfo, error) { return entry, nil }
func (entry relationFacadeAdversarialDirEntry) Size() int64                { return 0 }
func (entry relationFacadeAdversarialDirEntry) Mode() fs.FileMode          { return entry.mode }
func (entry relationFacadeAdversarialDirEntry) ModTime() time.Time         { return time.Time{} }
func (entry relationFacadeAdversarialDirEntry) Sys() any                   { return nil }

func verifyRelationFacadePhysicalInventory(t *testing.T, inventory relationFacadeInventory) {
	t.Helper()

	if !slices.Equal(inventory.names, relationFacadePhysicalFiles) {
		t.Fatalf("relation facade physical files = %q, want %q", inventory.names, relationFacadePhysicalFiles)
	}
	if inventory.bytes != relationFacadePhysicalBytes || inventory.digest != relationFacadePhysicalDigest {
		t.Fatalf("relation facade physical inventory = %d/%s, want %d/%s", inventory.bytes, inventory.digest, relationFacadePhysicalBytes, relationFacadePhysicalDigest)
	}
	generated := make(map[string][]byte, len(relationFacadeGeneratedFiles))
	for _, name := range relationFacadeGeneratedFiles {
		content, ok := inventory.files[name]
		if !ok {
			t.Fatalf("relation facade generated file %s is absent", name)
		}
		generated[name] = content
	}
	generatedBytes, generatedDigest := digestRelationFacadeFiles(generated)
	if len(generated) != 13 || generatedBytes != relationFacadeGeneratedBytes || generatedDigest != relationFacadeGeneratedDigest {
		t.Fatalf("relation facade generated inventory = %d/%d/%s, want 13/%d/%s", len(generated), generatedBytes, generatedDigest, relationFacadeGeneratedBytes, relationFacadeGeneratedDigest)
	}
}

func digestRelationFacadeFiles(files map[string][]byte) (int, string) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	digest := sha256.New()
	contentBytes := 0
	for _, name := range names {
		content := files[name]
		contentBytes += len(content)
		_, _ = io.WriteString(digest, name)
		_, _ = digest.Write([]byte{0})
		_, _ = io.WriteString(digest, strconv.Itoa(len(content)))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(content)
	}
	return contentBytes, fmt.Sprintf("%x", digest.Sum(nil))
}

func cloneRelationFacadeFiles(files map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(files)+1)
	for name, content := range files {
		cloned[name] = slices.Clone(content)
	}
	return cloned
}

func equalRelationFacadeFiles(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, content := range left {
		if !bytes.Equal(content, right[name]) {
			return false
		}
	}
	return true
}

func requireRelationFacadeTargetAbsent(t *testing.T, path, phase string) {
	t.Helper()

	_, err := os.Lstat(path)
	if err == nil {
		t.Fatalf("relation facade virtual target exists %s: %s", phase, path)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lstat relation facade virtual target %s: %v", phase, err)
	}
}

func replaceRelationFacadeToken(t *testing.T, source, oldToken, newToken []byte) []byte {
	t.Helper()

	if count := bytes.Count(source, oldToken); count != 1 {
		t.Fatalf("relation facade mutation token %q count = %d, want exact 1", oldToken, count)
	}
	return bytes.Replace(source, oldToken, newToken, 1)
}

func formatRelationFacadeMutation(t *testing.T, source []byte) []byte {
	t.Helper()

	formatted, err := format.Source(source)
	if err != nil {
		t.Fatalf("format relation facade adversarial mutation: %v", err)
	}
	return formatted
}

func verifyRelationFacadeOverlayGoList(t *testing.T, root, overlayPath, virtualName string) {
	t.Helper()

	command := exec.CommandContext(
		t.Context(),
		"go",
		"list",
		"-deps",
		"-test",
		"-json",
		"-mod=readonly",
		"-overlay="+overlayPath,
		modulePath+"/conformance/relationdeleteproduct",
	)
	command.Dir = root
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list relation facade overlay package closure: %v\n%s", err, output)
	}
	type packageFiles struct {
		prefix    string
		goFiles   []string
		testFiles []string
	}
	wantPackages := map[string]packageFiles{
		modulePath + "/conformance/relationdeleteproduct": {
			goFiles:   []string{"observer.go"},
			testFiles: []string{"product_test.go"},
		},
		modulePath + "/conformance/relationdeleteproduct/authors": {
			prefix: "authors",
			goFiles: []string{
				"zz_godj_generated.go",
				"zz_godj_relation.go",
				"zz_godj_relation_object.go",
				"zz_godj_relation_projection.go",
			},
		},
		modulePath + "/conformance/relationdeleteproduct/blog": {
			prefix: "blog",
			goFiles: []string{
				"zz_godj_generated.go",
				"zz_godj_relation.go",
				"zz_godj_relation_object.go",
				"zz_godj_relation_projection.go",
				"zz_godj_relation_query.go",
			},
		},
		modulePath + "/conformance/relationdeleteproduct/fixture": {
			prefix:  "fixture",
			goFiles: []string{"schema.go"},
		},
		modulePath + "/conformance/relationdeleteproduct/project": {
			prefix: "project",
			goFiles: []string{
				virtualName,
				"zz_godj_bindings.go",
				"zz_godj_relation_delete.go",
				"zz_godj_relation_object.go",
				"zz_godj_relation_select_related.go",
			},
		},
	}
	for path, files := range wantPackages {
		slices.Sort(files.goFiles)
		slices.Sort(files.testFiles)
		wantPackages[path] = files
	}
	seenPackages := make(map[string]bool, len(wantPackages))
	logicalFiles := make([]string, 0, 17)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath   string
			Dir          string
			GoFiles      []string
			CgoFiles     []string
			TestGoFiles  []string
			XTestGoFiles []string
		}
		if err := decoder.Decode(&listed); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode relation facade overlay go list: %v", err)
		}
		want, expected := wantPackages[listed.ImportPath]
		if !expected {
			continue
		}
		if seenPackages[listed.ImportPath] {
			t.Fatalf("relation facade overlay go list repeated package %s", listed.ImportPath)
		}
		seenPackages[listed.ImportPath] = true
		listedDirectory, err := filepath.EvalSymlinks(listed.Dir)
		if err != nil {
			t.Fatalf("canonicalize relation facade listed directory %s: %v", listed.ImportPath, err)
		}
		wantDirectory := filepath.Clean(filepath.Join(root, "conformance", "relationdeleteproduct", want.prefix))
		if listedDirectory != wantDirectory {
			t.Fatalf("relation facade package %s directory = %s, want %s", listed.ImportPath, listedDirectory, wantDirectory)
		}
		goFiles := slices.Clone(listed.GoFiles)
		testFiles := slices.Clone(listed.TestGoFiles)
		slices.Sort(goFiles)
		slices.Sort(testFiles)
		if !slices.Equal(goFiles, want.goFiles) || !slices.Equal(testFiles, want.testFiles) {
			t.Fatalf("relation facade package %s files = Go %q Test %q, want Go %q Test %q", listed.ImportPath, goFiles, testFiles, want.goFiles, want.testFiles)
		}
		if len(listed.CgoFiles) != 0 || len(listed.XTestGoFiles) != 0 {
			t.Fatalf("relation facade package %s has unexpected Cgo/XTest files: %q/%q", listed.ImportPath, listed.CgoFiles, listed.XTestGoFiles)
		}
		for _, name := range append(goFiles, testFiles...) {
			logicalFiles = append(logicalFiles, filepath.ToSlash(filepath.Join(want.prefix, name)))
		}
	}
	if len(seenPackages) != len(wantPackages) {
		t.Fatalf("relation facade overlay package closure = %d packages, want exact %d", len(seenPackages), len(wantPackages))
	}
	slices.Sort(logicalFiles)
	wantLogicalFiles := slices.Clone(relationFacadePhysicalFiles)
	wantLogicalFiles = append(wantLogicalFiles, filepath.ToSlash(filepath.Join("project", virtualName)))
	slices.Sort(wantLogicalFiles)
	if len(logicalFiles) != 17 || !slices.Equal(logicalFiles, wantLogicalFiles) {
		t.Fatalf("relation facade command-view files = %q, want logical exact 17 %q", logicalFiles, wantLogicalFiles)
	}
}

func validateRelationFacadeOverlaySource(source []byte) error {
	file, err := parseRelationFacadeSource("project_facade_spike.go", source, "project")
	if err != nil {
		return err
	}
	allowedImports := map[string]map[string]bool{
		"context": {"Context": true},
		modulePath + "/conformance/relationdeleteproduct/blog": {
			"Post":        true,
			"PostObjects": true,
		},
		modulePath + "/db": {
			"Queryer": true,
		},
		modulePath + "/orm": {
			"Ordering":  true,
			"Predicate": true,
			"QuerySet":  true,
		},
	}
	if err := validateRelationFacadeImports(file, allowedImports, "overlay"); err != nil {
		return err
	}

	wantTypes := map[string]string{
		"relationFacadeAuthorToken":       "struct{}",
		"BlogPostRelationFacadeSelectors": "struct { Author relationFacadeAuthorToken }",
		"BlogPostFacadeQuery":             "struct { Related BlogPostRelationFacadeSelectors; spikeBackend db.Queryer; spikeFactory BlogPostObjectFactory; spikeQuerySet orm.QuerySet[blog.Post] }",
		"BlogPostAuthorFacadeQuery":       "struct { spikeFactory BlogPostObjectFactory; spikeQuerySet orm.QuerySet[blog.Post] }",
		"Models":                          "struct { BlogPosts BlogPostFacadeQuery }",
	}
	wantFunctions := map[string]string{
		".Using":                            "func(backend db.Queryer) (Models, error)",
		"BlogPostFacadeQuery.Filter":        "func(predicates ...orm.Predicate[blog.Post]) BlogPostFacadeQuery",
		"BlogPostFacadeQuery.OrderBy":       "func(orderings ...orm.Ordering[blog.Post]) BlogPostFacadeQuery",
		"BlogPostFacadeQuery.Limit":         "func(limit int) (BlogPostFacadeQuery, error)",
		"BlogPostFacadeQuery.First":         "func(ctx context.Context) (*BlogPostObject, bool, error)",
		"BlogPostFacadeQuery.All":           "func(ctx context.Context) ([]*BlogPostObject, error)",
		"BlogPostFacadeQuery.SelectRelated": "func(relationFacadeAuthorToken) BlogPostAuthorFacadeQuery",
		"BlogPostAuthorFacadeQuery.Filter":  "func(predicates ...orm.Predicate[blog.Post]) BlogPostAuthorFacadeQuery",
		"BlogPostAuthorFacadeQuery.OrderBy": "func(orderings ...orm.Ordering[blog.Post]) BlogPostAuthorFacadeQuery",
		"BlogPostAuthorFacadeQuery.Limit":   "func(limit int) (BlogPostAuthorFacadeQuery, error)",
		"BlogPostAuthorFacadeQuery.All":     "func(ctx context.Context) ([]*BlogPostObject, error)",
	}
	wantReceivers := map[string]string{
		".Using":                            "",
		"BlogPostFacadeQuery.Filter":        "query BlogPostFacadeQuery",
		"BlogPostFacadeQuery.OrderBy":       "query BlogPostFacadeQuery",
		"BlogPostFacadeQuery.Limit":         "query BlogPostFacadeQuery",
		"BlogPostFacadeQuery.First":         "query BlogPostFacadeQuery",
		"BlogPostFacadeQuery.All":           "query BlogPostFacadeQuery",
		"BlogPostFacadeQuery.SelectRelated": "query BlogPostFacadeQuery",
		"BlogPostAuthorFacadeQuery.Filter":  "query BlogPostAuthorFacadeQuery",
		"BlogPostAuthorFacadeQuery.OrderBy": "query BlogPostAuthorFacadeQuery",
		"BlogPostAuthorFacadeQuery.Limit":   "query BlogPostAuthorFacadeQuery",
		"BlogPostAuthorFacadeQuery.All":     "query BlogPostAuthorFacadeQuery",
	}
	seenTypes := make(map[string]bool, len(wantTypes))
	seenFunctions := make(map[string]bool, len(wantFunctions))
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			if declaration.Tok == token.IMPORT {
				continue
			}
			if declaration.Tok != token.TYPE {
				return fmt.Errorf("forbidden overlay declaration %s", declaration.Tok)
			}
			for _, specification := range declaration.Specs {
				typeSpecification, ok := specification.(*ast.TypeSpec)
				if !ok {
					return fmt.Errorf("forbidden overlay non-type specification")
				}
				if typeSpecification.Assign.IsValid() || typeSpecification.TypeParams != nil {
					return fmt.Errorf("forbidden overlay alias or generic type declaration %q", typeSpecification.Name.Name)
				}
				wantSchema, expected := wantTypes[typeSpecification.Name.Name]
				if !expected || seenTypes[typeSpecification.Name.Name] {
					return fmt.Errorf("forbidden or duplicate overlay type declaration")
				}
				if err := validateRelationFacadeExpressionSchema(typeSpecification.Type, wantSchema); err != nil {
					return fmt.Errorf("overlay type schema %s: %w", typeSpecification.Name.Name, err)
				}
				seenTypes[typeSpecification.Name.Name] = true
			}
		case *ast.FuncDecl:
			key, err := relationFacadeFunctionKey(declaration)
			if err != nil {
				return err
			}
			wantSignature, expected := wantFunctions[key]
			if !expected || seenFunctions[key] {
				return fmt.Errorf("forbidden or duplicate overlay function %q", key)
			}
			if err := validateRelationFacadeExpressionSchema(declaration.Type, wantSignature); err != nil {
				return fmt.Errorf("overlay function signature %s: %w", key, err)
			}
			if err := validateRelationFacadeReceiverSchema(declaration, wantReceivers[key]); err != nil {
				return fmt.Errorf("overlay receiver schema %s: %w", key, err)
			}
			seenFunctions[key] = true
		default:
			return fmt.Errorf("forbidden overlay declaration %T", declaration)
		}
	}
	if len(seenTypes) != len(wantTypes) || len(seenFunctions) != len(wantFunctions) {
		return fmt.Errorf("overlay declaration set is incomplete: types=%d/%d functions=%d/%d", len(seenTypes), len(wantTypes), len(seenFunctions), len(wantFunctions))
	}

	importSelectors := relationFacadeImportSelectors(file, allowedImports)
	allowedSelectors := map[string]bool{
		"All":           true,
		"Author":        true,
		"BlogPost":      true,
		"Filter":        true,
		"First":         true,
		"From":          true,
		"Limit":         true,
		"OrderBy":       true,
		"SelectRelated": true,
		"Using":         true,
		"spikeBackend":  true,
		"spikeFactory":  true,
		"spikeQuerySet": true,
	}
	forbiddenCompositeTypes := map[string]bool{
		"BlogPostAuthorSelectRelatedQuery":   true,
		"BlogPostDynamicSelectRelatedQuery":  true,
		"BlogPostObject":                     true,
		"BlogPostObjectFactory":              true,
		"BlogPostReviewerObjectRelation":     true,
		"BlogPostReviewerSelectRelatedQuery": true,
		"BlogPostSelectRelated":              true,
		"Objects":                            true,
		"RelationDeleters":                   true,
	}
	allowedCompositeTypes := map[string]bool{
		"BlogPostAuthorFacadeQuery":       true,
		"BlogPostFacadeQuery":             true,
		"BlogPostRelationFacadeSelectors": true,
		"Models":                          true,
	}
	allowedBareCalls := map[string]bool{"BindObjects": true, "len": true, "make": true}
	forbiddenIdentifiers := map[string]bool{
		"BindRelationDeleters":                             true,
		"BlogPostAuthorSelectRelatedQuery":                 true,
		"BlogPostDynamicSelectRelatedQuery":                true,
		"BlogPostReviewerObjectRelation":                   true,
		"BlogPostReviewerSelectRelatedQuery":               true,
		"BlogPostSelectRelated":                            true,
		"Delete":                                           true,
		"GoDjProjectBindingGeneratorVersion":               true,
		"GoDjProjectRelationDeleteGeneratorVersion":        true,
		"GoDjProjectRelationObjectGeneratorVersion":        true,
		"GoDjProjectRelationSelectRelatedGeneratorVersion": true,
		"MarshalJSON":                                      true,
		"Objects":                                          true,
		"ParseDynamic":                                     true,
		"RelationDeleters":                                 true,
		"RelationSetNull":                                  true,
		"Reviewer":                                         true,
		"Save":                                             true,
		"UnmarshalJSON":                                    true,
	}
	forbiddenLocalBindings := map[string]bool{"BindObjects": true}
	for qualifier := range importSelectors {
		forbiddenLocalBindings[qualifier] = true
	}
	bindObjectsCalls := 0
	postObjectsUsingCalls := 0
	var validationErr error
	ast.Inspect(file, func(node ast.Node) bool {
		if validationErr != nil {
			return false
		}
		switch node := node.(type) {
		case *ast.FuncLit:
			validationErr = fmt.Errorf("forbidden overlay function literal")
			return false
		case *ast.CallExpr:
			switch function := node.Fun.(type) {
			case *ast.Ident:
				if !allowedBareCalls[function.Name] {
					validationErr = fmt.Errorf("forbidden overlay call %q", function.Name)
					return false
				}
				if function.Name == "BindObjects" {
					bindObjectsCalls++
				}
			case *ast.SelectorExpr:
				if function.Sel.Name == "Using" && relationFacadeSelectorPath(function.X) == "blog.PostObjects" {
					postObjectsUsingCalls++
				}
			default:
				validationErr = fmt.Errorf("forbidden overlay call expression %T", function)
				return false
			}
		case *ast.SelectorExpr:
			if qualifier, ok := node.X.(*ast.Ident); ok {
				if allowed, imported := importSelectors[qualifier.Name]; imported {
					if !allowed[node.Sel.Name] {
						validationErr = fmt.Errorf("forbidden overlay imported selector %s.%s", qualifier.Name, node.Sel.Name)
						return false
					}
					return true
				}
			}
			if !allowedSelectors[node.Sel.Name] {
				validationErr = fmt.Errorf("forbidden overlay selector %q", node.Sel.Name)
				return false
			}
		case *ast.CompositeLit:
			name := relationFacadeTypeName(node.Type)
			if forbiddenCompositeTypes[name] {
				validationErr = fmt.Errorf("forbidden overlay composite literal %q", name)
				return false
			}
			if name != "" && !allowedCompositeTypes[name] {
				validationErr = fmt.Errorf("forbidden overlay composite literal %q", name)
				return false
			}
		case *ast.Ident:
			if forbiddenLocalBindings[node.Name] && node.Obj != nil {
				validationErr = fmt.Errorf("forbidden overlay local shadow %q", node.Name)
				return false
			}
			if forbiddenIdentifiers[node.Name] {
				validationErr = fmt.Errorf("forbidden overlay identifier %q", node.Name)
				return false
			}
		}
		return true
	})
	if validationErr != nil {
		return validationErr
	}
	if bindObjectsCalls != 1 || postObjectsUsingCalls != 1 {
		return fmt.Errorf("overlay kernel calls BindObjects/PostObjects.Using = %d/%d, want 1/1", bindObjectsCalls, postObjectsUsingCalls)
	}
	return nil
}

func validateRelationFacadeConsumerSource(source []byte) error {
	file, err := parseRelationFacadeSource("external_consumer.go", source, "facadeconsumer")
	if err != nil {
		return err
	}
	allowedImports := map[string]map[string]bool{
		"context": {"Context": true},
		modulePath + "/conformance/relationdeleteproduct/authors": {
			"Author": true,
		},
		modulePath + "/conformance/relationdeleteproduct/blog": {
			"Post":       true,
			"PostFields": true,
		},
		modulePath + "/conformance/relationdeleteproduct/project": {
			"BlogPostObject": true,
			"Using":          true,
		},
		modulePath + "/db": {
			"Queryer":         true,
			"RelationAtomic":  true,
			"RelationSession": true,
		},
	}
	if err := validateRelationFacadeImports(file, allowedImports, "consumer"); err != nil {
		return err
	}

	wantFunctions := map[string]string{
		".compileRelationFacade":  "func(ctx context.Context, backend db.Queryer) error",
		".compileRelationSession": "func(ctx context.Context, atomic db.RelationAtomic) error",
	}
	seenFunctions := make(map[string]bool, len(wantFunctions))
	var mainFunction *ast.FuncDecl
	var sessionFunction *ast.FuncDecl
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			if declaration.Tok != token.IMPORT {
				return fmt.Errorf("forbidden consumer declaration %s", declaration.Tok)
			}
		case *ast.FuncDecl:
			key, err := relationFacadeFunctionKey(declaration)
			if err != nil {
				return err
			}
			wantSignature, expected := wantFunctions[key]
			if !expected || seenFunctions[key] {
				return fmt.Errorf("forbidden or duplicate consumer function %q", key)
			}
			if err := validateRelationFacadeExpressionSchema(declaration.Type, wantSignature); err != nil {
				return fmt.Errorf("consumer function signature %s: %w", key, err)
			}
			seenFunctions[key] = true
			switch key {
			case ".compileRelationFacade":
				mainFunction = declaration
			case ".compileRelationSession":
				sessionFunction = declaration
			}
		default:
			return fmt.Errorf("forbidden consumer declaration %T", declaration)
		}
	}
	if len(seenFunctions) != len(wantFunctions) {
		return fmt.Errorf("consumer function set is incomplete: %d/%d", len(seenFunctions), len(wantFunctions))
	}
	if err := validateRelationFacadeMainConsumer(mainFunction); err != nil {
		return err
	}
	if err := validateRelationFacadeSessionConsumer(sessionFunction); err != nil {
		return err
	}

	importSelectors := relationFacadeImportSelectors(file, allowedImports)
	allowedSelectors := map[string]bool{
		"All":            true,
		"Asc":            true,
		"AtomicRelation": true,
		"Author":         true,
		"AuthorID":       true,
		"BlogPosts":      true,
		"Filter":         true,
		"First":          true,
		"IContains":      true,
		"ID":             true,
		"Limit":          true,
		"Model":          true,
		"Name":           true,
		"OrderBy":        true,
		"Related":        true,
		"SelectRelated":  true,
		"Title":          true,
	}
	projectUsingCalls := 0
	relatedAuthorTokens := 0
	eagerAssignments := 0
	var validationErr error
	ast.Inspect(file, func(node ast.Node) bool {
		if validationErr != nil {
			return false
		}
		switch node := node.(type) {
		case *ast.CallExpr:
			if function, ok := node.Fun.(*ast.Ident); ok && function.Name != "len" {
				validationErr = fmt.Errorf("forbidden consumer call %q", function.Name)
				return false
			}
			if function, ok := node.Fun.(*ast.SelectorExpr); ok && function.Sel.Name == "Using" && relationFacadeSelectorPath(function.X) == "project" {
				projectUsingCalls++
			}
		case *ast.SelectorExpr:
			if qualifier, ok := node.X.(*ast.Ident); ok {
				if allowed, imported := importSelectors[qualifier.Name]; imported {
					if !allowed[node.Sel.Name] {
						validationErr = fmt.Errorf("forbidden consumer imported selector %s.%s", qualifier.Name, node.Sel.Name)
						return false
					}
					return true
				}
			}
			if !allowedSelectors[node.Sel.Name] {
				validationErr = fmt.Errorf("forbidden consumer selector %q", node.Sel.Name)
				return false
			}
			if node.Sel.Name == "Author" && relationFacadeSelectorPath(node.X) == "models.BlogPosts.Related" {
				relatedAuthorTokens++
			}
		case *ast.AssignStmt:
			if node.Tok != token.ASSIGN || len(node.Lhs) != 1 || len(node.Rhs) != 1 {
				return true
			}
			left, leftOK := node.Lhs[0].(*ast.Ident)
			indexed, rightOK := node.Rhs[0].(*ast.IndexExpr)
			if !rightOK {
				return true
			}
			base, baseOK := indexedExpressionName(indexed)
			index, indexOK := indexed.Index.(*ast.BasicLit)
			if leftOK && left.Name == "post" && rightOK && baseOK && base == "posts" && indexOK && index.Kind == token.INT && index.Value == "0" {
				eagerAssignments++
			}
		}
		return true
	})
	if validationErr != nil {
		return validationErr
	}
	if projectUsingCalls != 2 {
		return fmt.Errorf("consumer project.Using call sites = %d, want exact 2", projectUsingCalls)
	}
	if relatedAuthorTokens != 1 {
		return fmt.Errorf("consumer Related.Author selector tokens = %d, want exact 1", relatedAuthorTokens)
	}
	if eagerAssignments != 1 {
		return fmt.Errorf("consumer eager post = posts[0] assignments = %d, want exact 1", eagerAssignments)
	}
	return nil
}

func validateRelationFacadeMainConsumer(function *ast.FuncDecl) error {
	if err := validateRelationFacadeFunctionCalls(function, map[string]int{
		"All":           1,
		"Asc":           3,
		"Author":        2,
		"Filter":        2,
		"First":         1,
		"IContains":     2,
		"Limit":         2,
		"Model":         1,
		"OrderBy":       3,
		"SelectRelated": 1,
		"Using":         1,
	}, map[string]int{"len": 1}, "main consumer"); err != nil {
		return err
	}
	if count := countRelationFacadeAssignments(
		function,
		token.DEFINE,
		[]string{"models", "err"},
		[]string{"project.Using(backend)"},
	); count != 1 {
		return fmt.Errorf("main consumer project.Using(backend) assignments = %d, want exact 1", count)
	}
	if count := countRelationFacadeAssignments(
		function,
		token.DEFINE,
		[]string{"post", "found", "err"},
		[]string{"models.BlogPosts.OrderBy(blog.PostFields.ID.Asc()).First(ctx)"},
	); count != 1 {
		return fmt.Errorf("main consumer direct ordered First assignments = %d, want exact 1", count)
	}
	for _, assignment := range []struct {
		label string
		left  []string
		right []string
	}{
		{
			label: "separate Filter",
			left:  []string{"filtered"},
			right: []string{`models.BlogPosts.Filter(blog.PostFields.Title.IContains("lazy"))`},
		},
		{
			label: "separate OrderBy",
			left:  []string{"ordered"},
			right: []string{"filtered.OrderBy(blog.PostFields.ID.Asc())"},
		},
		{
			label: "separate Limit",
			left:  []string{"limited", "err"},
			right: []string{"ordered.Limit(1)"},
		},
	} {
		if count := countRelationFacadeAssignments(function, token.DEFINE, assignment.left, assignment.right); count != 1 {
			return fmt.Errorf("main consumer %s assignments = %d, want exact 1", assignment.label, count)
		}
	}
	if count := countRelationFacadeAssignments(function, token.ASSIGN, []string{"post"}, []string{"posts[0]"}); count != 1 {
		return fmt.Errorf("main consumer eager post = posts[0] assignments = %d, want exact 1", count)
	}
	return nil
}

func validateRelationFacadeSessionConsumer(function *ast.FuncDecl) error {
	if function == nil || function.Body == nil {
		return fmt.Errorf("session consumer function body is absent")
	}
	functionLiterals := 0
	goStatements := 0
	deferStatements := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.FuncLit:
			functionLiterals++
		case *ast.GoStmt:
			goStatements++
		case *ast.DeferStmt:
			deferStatements++
		}
		return true
	})
	if goStatements != 0 {
		return fmt.Errorf("session consumer contains forbidden GoStmt count %d", goStatements)
	}
	if deferStatements != 0 {
		return fmt.Errorf("session consumer contains forbidden DeferStmt count %d", deferStatements)
	}
	if functionLiterals != 1 {
		return fmt.Errorf("session consumer function literals = %d, want exact one AtomicRelation callback", functionLiterals)
	}
	if err := validateRelationFacadeFunctionCalls(function, map[string]int{
		"Asc":            1,
		"AtomicRelation": 1,
		"First":          1,
		"OrderBy":        1,
		"Using":          1,
	}, nil, "session consumer"); err != nil {
		return err
	}
	if len(function.Body.List) != 1 {
		return fmt.Errorf("session consumer body statements = %d, want one returned AtomicRelation call", len(function.Body.List))
	}
	returned, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return fmt.Errorf("session consumer does not return one AtomicRelation call")
	}
	call, ok := returned.Results[0].(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("session consumer return is not a call")
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "AtomicRelation" || relationFacadeSelectorPath(selector.X) != "atomic" {
		return fmt.Errorf("session consumer return is not atomic.AtomicRelation")
	}
	if len(call.Args) != 2 || !relationFacadeExpressionMatches(call.Args[0], "ctx") {
		return fmt.Errorf("session AtomicRelation arguments are not exact ctx/callback")
	}
	callback, ok := call.Args[1].(*ast.FuncLit)
	if !ok {
		return fmt.Errorf("session AtomicRelation callback is not a function literal")
	}
	if err := validateRelationFacadeExpressionSchema(callback.Type, "func(session db.RelationSession) error"); err != nil {
		return fmt.Errorf("session AtomicRelation callback signature: %w", err)
	}
	if len(callback.Body.List) != 4 {
		return fmt.Errorf("session callback body statements = %d, want exact synchronous sequence of 4", len(callback.Body.List))
	}
	if !relationFacadeAssignmentStatementMatches(
		callback.Body.List[0],
		token.DEFINE,
		[]string{"models", "err"},
		[]string{"project.Using(session)"},
	) {
		return fmt.Errorf("session callback project.Using(session) assignments = 0, want exact first statement")
	}
	checked, ok := callback.Body.List[1].(*ast.IfStmt)
	if !ok || checked.Init != nil || checked.Else != nil || !relationFacadeExpressionMatches(checked.Cond, "err != nil") || len(checked.Body.List) != 1 || !relationFacadeReturnStatementMatches(checked.Body.List[0], []string{"err"}) {
		return fmt.Errorf("session callback error check is not exact if err != nil { return err }")
	}
	if !relationFacadeAssignmentStatementMatches(
		callback.Body.List[2],
		token.ASSIGN,
		[]string{"_", "_", "err"},
		[]string{"models.BlogPosts.OrderBy(blog.PostFields.ID.Asc()).First(ctx)"},
	) {
		return fmt.Errorf("session callback query assignment is not exact synchronous ordered First")
	}
	if !relationFacadeReturnStatementMatches(callback.Body.List[3], []string{"err"}) {
		return fmt.Errorf("session callback final statement is not return err")
	}
	return nil
}

func validateRelationFacadeFunctionCalls(
	function *ast.FuncDecl,
	wantSelectors map[string]int,
	wantBare map[string]int,
	label string,
) error {
	if function == nil || function.Body == nil {
		return fmt.Errorf("%s function body is absent", label)
	}
	selectors := make(map[string]int, len(wantSelectors))
	bare := make(map[string]int, len(wantBare))
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch called := call.Fun.(type) {
		case *ast.SelectorExpr:
			selectors[called.Sel.Name]++
		case *ast.Ident:
			bare[called.Name]++
		}
		return true
	})
	if !equalRelationFacadeCallCounts(selectors, wantSelectors) {
		return fmt.Errorf("%s selector call counts = %#v, want %#v", label, selectors, wantSelectors)
	}
	if !equalRelationFacadeCallCounts(bare, wantBare) {
		return fmt.Errorf("%s bare call counts = %#v, want %#v", label, bare, wantBare)
	}
	return nil
}

func equalRelationFacadeCallCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for name, count := range want {
		if got[name] != count {
			return false
		}
	}
	return true
}

func countRelationFacadeAssignments(function *ast.FuncDecl, operation token.Token, left, right []string) int {
	return countRelationFacadeAssignmentsIn(function.Body, operation, left, right)
}

func countRelationFacadeAssignmentsIn(root ast.Node, operation token.Token, left, right []string) int {
	count := 0
	ast.Inspect(root, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if ok && relationFacadeAssignmentMatches(assignment, operation, left, right) {
			count++
		}
		return true
	})
	return count
}

func relationFacadeAssignmentStatementMatches(statement ast.Stmt, operation token.Token, left, right []string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	return ok && relationFacadeAssignmentMatches(assignment, operation, left, right)
}

func relationFacadeAssignmentMatches(assignment *ast.AssignStmt, operation token.Token, left, right []string) bool {
	if assignment == nil || assignment.Tok != operation || len(assignment.Lhs) != len(left) || len(assignment.Rhs) != len(right) {
		return false
	}
	for index, want := range left {
		identifier, ok := assignment.Lhs[index].(*ast.Ident)
		if !ok || identifier.Name != want {
			return false
		}
	}
	for index, want := range right {
		if !relationFacadeExpressionMatches(assignment.Rhs[index], want) {
			return false
		}
	}
	return true
}

func relationFacadeReturnStatementMatches(statement ast.Stmt, expressions []string) bool {
	returned, ok := statement.(*ast.ReturnStmt)
	if !ok || len(returned.Results) != len(expressions) {
		return false
	}
	for index, want := range expressions {
		if !relationFacadeExpressionMatches(returned.Results[index], want) {
			return false
		}
	}
	return true
}

func relationFacadeExpressionMatches(expression ast.Expr, wantSource string) bool {
	want, err := parser.ParseExpr(wantSource)
	if err != nil {
		return false
	}
	gotFormatted, err := formatRelationFacadeExpression(expression)
	if err != nil {
		return false
	}
	wantFormatted, err := formatRelationFacadeExpression(want)
	return err == nil && gotFormatted == wantFormatted
}

func parseRelationFacadeSource(name string, source []byte, wantPackage string) (*ast.File, error) {
	formatted, err := format.Source(source)
	if err != nil {
		return nil, fmt.Errorf("format %s: %w", name, err)
	}
	if !bytes.Equal(formatted, source) {
		return nil, fmt.Errorf("%s is not gofmt-stable", name)
	}
	file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if file.Name.Name != wantPackage {
		return nil, fmt.Errorf("%s package = %q, want %q", name, file.Name.Name, wantPackage)
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(strings.TrimSpace(comment.Text), "//go:") {
				return nil, fmt.Errorf("%s contains forbidden Go directive", name)
			}
		}
	}
	return file, nil
}

func validateRelationFacadeExpressionSchema(expression ast.Expr, wantSource string) error {
	wantExpression, err := parser.ParseExpr(wantSource)
	if err != nil {
		return fmt.Errorf("parse expected schema %q: %w", wantSource, err)
	}
	got, err := formatRelationFacadeExpression(expression)
	if err != nil {
		return err
	}
	want, err := formatRelationFacadeExpression(wantExpression)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("got %q, want %q", got, want)
	}
	return nil
}

func validateRelationFacadeReceiverSchema(declaration *ast.FuncDecl, want string) error {
	if want == "" {
		if declaration.Recv != nil {
			return fmt.Errorf("got receiver, want none")
		}
		return nil
	}
	if declaration.Recv == nil || len(declaration.Recv.List) != 1 || len(declaration.Recv.List[0].Names) != 1 {
		return fmt.Errorf("receiver shape is not one exact named value")
	}
	receiverType, err := formatRelationFacadeExpression(declaration.Recv.List[0].Type)
	if err != nil {
		return err
	}
	got := declaration.Recv.List[0].Names[0].Name + " " + receiverType
	if got != want {
		return fmt.Errorf("got %q, want %q", got, want)
	}
	return nil
}

func formatRelationFacadeExpression(expression ast.Expr) (string, error) {
	var formatted bytes.Buffer
	if err := format.Node(&formatted, token.NewFileSet(), expression); err != nil {
		return "", fmt.Errorf("format relation facade AST schema: %w", err)
	}
	return formatted.String(), nil
}

func validateRelationFacadeImports(file *ast.File, allowed map[string]map[string]bool, label string) error {
	seen := make(map[string]bool, len(allowed))
	for _, specification := range file.Imports {
		if specification.Name != nil {
			return fmt.Errorf("forbidden %s import alias %q", label, specification.Name.Name)
		}
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			return fmt.Errorf("decode %s import: %w", label, err)
		}
		if _, ok := allowed[path]; !ok {
			return fmt.Errorf("forbidden %s import %q", label, path)
		}
		if seen[path] {
			return fmt.Errorf("duplicate %s import %q", label, path)
		}
		seen[path] = true
	}
	if len(seen) != len(allowed) {
		return fmt.Errorf("%s import set = %d entries, want exact %d", label, len(seen), len(allowed))
	}
	return nil
}

func relationFacadeImportSelectors(file *ast.File, allowed map[string]map[string]bool) map[string]map[string]bool {
	selectors := make(map[string]map[string]bool, len(file.Imports))
	for _, specification := range file.Imports {
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			continue
		}
		qualifier := filepath.Base(path)
		allowedSelectors, ok := allowed[path]
		if !ok {
			continue
		}
		selectors[qualifier] = make(map[string]bool, len(allowedSelectors))
		for name := range allowedSelectors {
			selectors[qualifier][name] = true
		}
	}
	return selectors
}

func relationFacadeFunctionKey(declaration *ast.FuncDecl) (string, error) {
	receiver := ""
	if declaration.Recv != nil {
		if len(declaration.Recv.List) != 1 {
			return "", fmt.Errorf("relation facade function %s has invalid receiver", declaration.Name.Name)
		}
		receiver = relationFacadeTypeName(declaration.Recv.List[0].Type)
		if receiver == "" {
			return "", fmt.Errorf("relation facade function %s has unsupported receiver", declaration.Name.Name)
		}
	}
	return receiver + "." + declaration.Name.Name, nil
}

func relationFacadeTypeName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return relationFacadeTypeName(expression.X)
	case *ast.IndexExpr:
		return relationFacadeTypeName(expression.X)
	case *ast.IndexListExpr:
		return relationFacadeTypeName(expression.X)
	default:
		return ""
	}
}

func relationFacadeSelectorPath(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		prefix := relationFacadeSelectorPath(expression.X)
		if prefix == "" {
			return expression.Sel.Name
		}
		return prefix + "." + expression.Sel.Name
	default:
		return ""
	}
}

func indexedExpressionName(expression *ast.IndexExpr) (string, bool) {
	identifier, ok := expression.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return identifier.Name, true
}

func TestTypedAPIMisuseDoesNotCompile(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantFragments []string
	}{
		{
			name:    "predicate model mismatch",
			fixture: "predicate_model_mismatch.go.txt",
			wantFragments: []string{
				"models.ArticleFields.Title.Exact",
				"orm.Predicate[Other]",
			},
		},
		{
			name:    "descriptor model mismatch",
			fixture: "descriptor_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.ModelDescriptor[Other]",
				"wrong type for method",
			},
		},
		{
			name:    "descriptor clone is required",
			fixture: "descriptor_clone_missing.go.txt",
			wantFragments: []string{
				"orm.ModelDescriptor[CustomModel]",
				"missing method CloneModel",
			},
		},
		{
			name:    "isnull requires bool",
			fixture: "isnull_string.go.txt",
			wantFragments: []string{
				"cannot use \"true\"",
				"as bool value",
			},
		},
		{
			name:    "nullable exact requires value",
			fixture: "nullable_exact_pointer.go.txt",
			wantFragments: []string{
				"cannot use (*string)(nil)",
				"as string value",
			},
		},
		{
			name:    "icontains requires string",
			fixture: "icontains_integer.go.txt",
			wantFragments: []string{
				"cannot use 123",
				"as string value",
			},
		},
		{
			name:    "non-null field has no null builder",
			fixture: "write_title_null.go.txt",
			wantFragments: []string{
				"WithTitleNull undefined",
			},
		},
		{
			name:    "write scalar type is static",
			fixture: "write_wrong_scalar.go.txt",
			wantFragments: []string{
				"cannot use \"false\"",
				"as bool value",
			},
		},
		{
			name:    "write input model mismatch",
			fixture: "write_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.CreateInput[Other]",
				"wrong type for method BuildCreate",
			},
		},
		{
			name:    "Save update field model mismatch",
			fixture: "save_field_model_mismatch.go.txt",
			wantFragments: []string{
				"cannot use orm.NewStringField[Other]",
				"orm.WritableField[models.Article]",
			},
		},
		{
			name:    "Save primary key is not writable",
			fixture: "save_primary_key_mask.go.txt",
			wantFragments: []string{
				"models.ArticleFields.ID",
				"orm.WritableField[models.Article]",
			},
		},
		{
			name:    "Save option model mismatch",
			fixture: "save_option_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.ForceInsert[Other]()",
				"orm.SaveOption[models.Article]",
			},
		},
		{
			name:    "QuerySet Iterate callback model mismatch",
			fixture: "query_iterate_model_mismatch.go.txt",
			wantFragments: []string{
				"cannot use func(Other) error",
				"func(models.Article) error",
			},
		},
		{
			name:    "QuerySet terminal result model mismatch",
			fixture: "query_terminal_result_mismatch.go.txt",
			wantFragments: []string{
				"cannot use article",
				"as Other value",
			},
		},
		{
			name:    "related predicate source model mismatch",
			fixture: "relation_query/predicate_source_mismatch.go.txt",
			wantFragments: []string{
				"relations.BlogPost.Author.Name.Exact",
				"orm.Predicate[authors.Author]",
			},
		},
		{
			name:    "forward relation target field mismatch",
			fixture: "relation_query/target_field_mismatch.go.txt",
			wantFragments: []string{
				"blog.PostFields.Title",
				"orm.StringField[authors.Author]",
			},
		},
		{
			name:    "related integer exact requires integer",
			fixture: "relation_query/integer_value_mismatch.go.txt",
			wantFragments: []string{
				"cannot use \"1\"",
				"as int64 value",
			},
		},
		{
			name:    "relation object predicate keeps source model",
			fixture: "relation_object/predicate_source_mismatch.go.txt",
			wantFragments: []string{
				"objects.BlogPost.Reviewer.IsNull",
				"orm.Predicate[authors.Author]",
			},
		},
		{
			name:    "relation object factory requires source model",
			fixture: "relation_object/factory_source_mismatch.go.txt",
			wantFragments: []string{
				"cannot use author",
				"as blog.Post value",
			},
		},
		{
			name:    "relation object isnull requires bool",
			fixture: "relation_object/isnull_value_mismatch.go.txt",
			wantFragments: []string{
				"cannot use \"true\"",
				"as bool value",
			},
		},
		{
			name:    "reverse relation predicate keeps owner model",
			fixture: "relation_reverse/predicate_owner_mismatch.go.txt",
			wantFragments: []string{
				"relations.AuthorsAuthor.Posts.Title.Exact",
				"orm.Predicate[blog.Post]",
			},
		},
		{
			name:    "select-related source QuerySet keeps source model",
			fixture: "relation_select_related/source_queryset_mismatch.go.txt",
			wantFragments: []string{
				"cannot use authors.AuthorObjects.Using(backend)",
				"orm.QuerySet[blog.Post]",
			},
		},
		{
			name:    "select-related remains singular",
			fixture: "relation_select_related/multiple_selection.go.txt",
			wantFragments: []string{
				"Author().Reviewer undefined",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := compileFixture(t, test.fixture)
			if result.err == nil {
				t.Fatalf("fixture %s unexpectedly compiled", test.fixture)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(result.output, fragment) {
					t.Fatalf("compiler output for %s does not contain %q:\n%s", test.fixture, fragment, result.output)
				}
			}
		})
	}
}

func TestDirectPackageDependencyBoundaries(t *testing.T) {
	forbidden := []dependencyEdge{
		{from: modulePath + "/schema/ir", to: modulePath + "/orm"},
		{from: modulePath + "/query", to: modulePath + "/orm"},
		{from: modulePath + "/orm", to: modulePath + "/db/sqlite"},
		{from: modulePath + "/codegen", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/examples/article/models", to: modulePath + "/codegen"},
		{from: modulePath + "/internal/cmd/m1generate", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/migrations", to: modulePath + "/migrations/definition"},
		{from: modulePath + "/internal/projectcheck", to: modulePath + "/internal/projectcheck/linked"},
		{from: modulePath + "/internal/projectcheck/linked", to: modulePath + "/internal/projectcheck"},
		{from: modulePath + "/migrations", to: modulePath + "/internal/projectcheck"},
		{from: modulePath + "/migrations", to: modulePath + "/internal/projectcheck/linked"},
		{from: modulePath + "/migrations/definition", to: modulePath + "/internal/projectcheck/linked"},
	}

	packages := make([]string, 0, len(forbidden))
	for _, edge := range forbidden {
		if !slices.Contains(packages, edge.from) {
			packages = append(packages, edge.from)
		}
	}

	root := repositoryRoot(t)
	arguments := append([]string{"list", "-json"}, packages...)
	command := exec.Command("go", arguments...)
	command.Dir = root
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("load direct package imports: %v\n%s", err, output)
	}

	directImports := make(map[string][]string, len(packages))
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath string
			Imports    []string
		}
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		directImports[listed.ImportPath] = listed.Imports
	}

	for _, edge := range forbidden {
		imports, ok := directImports[edge.from]
		if !ok {
			t.Errorf("go list did not return package %s", edge.from)
			continue
		}
		if slices.Contains(imports, edge.to) {
			t.Errorf("forbidden direct dependency exists: %s -> %s", edge.from, edge.to)
		}
	}
}

func TestProjectCheckDirectImportGraph(t *testing.T) {
	want := map[string][]string{
		modulePath + "/project": {
			modulePath + "/internal/projectcheck/linked",
		},
		modulePath + "/internal/projectcheck": {
			modulePath + "/internal/projectcheck/protocol",
		},
		modulePath + "/internal/projectcheck/linked": {
			modulePath + "/internal/projectcheck/protocol",
			modulePath + "/migrations",
			modulePath + "/migrations/definition",
		},
		modulePath + "/internal/projectcheck/protocol": nil,
	}

	packages := make([]string, 0, len(want))
	for packagePath := range want {
		packages = append(packages, packagePath)
	}
	slices.Sort(packages)
	command := exec.Command("go", append([]string{"list", "-json"}, packages...)...)
	command.Dir = repositoryRoot(t)
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("load project-check imports: %v\n%s", err, output)
	}

	seen := make(map[string]bool, len(want))
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath string
			Imports    []string
		}
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode project-check package: %v", err)
		}
		required, expected := want[listed.ImportPath]
		if !expected {
			continue
		}
		seen[listed.ImportPath] = true
		for _, requiredImport := range required {
			if !slices.Contains(listed.Imports, requiredImport) {
				t.Errorf("%s does not import required package %s", listed.ImportPath, requiredImport)
			}
		}
		for _, imported := range listed.Imports {
			if !strings.HasPrefix(imported, modulePath+"/") {
				continue
			}
			if !slices.Contains(required, imported) {
				t.Errorf("%s has unexpected direct module import %s", listed.ImportPath, imported)
			}
		}
	}
	for packagePath := range want {
		if !seen[packagePath] {
			t.Errorf("go list did not return %s", packagePath)
		}
	}
}

type compileResult struct {
	output string
	err    error
}

func compileFixture(t *testing.T, fixture string) compileResult {
	t.Helper()

	root := repositoryRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "internal", "compiletest", "testdata", fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}

	directory := t.TempDir()
	goMod := fmt.Sprintf(`module example.com/godj-compile-gate

go 1.26.0

require %s v0.0.0

replace %s => %s
`, modulePath, modulePath, filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write fixture go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "consumer.go"), source, 0o644); err != nil {
		t.Fatalf("write fixture source: %v", err)
	}

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

type dependencyEdge struct {
	from string
	to   string
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve compile test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func commandEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOFLAGS=") || strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOTOOLCHAIN=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
	return environment
}
