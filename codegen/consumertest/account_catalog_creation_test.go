package codegen_test

import (
	_ "embed"
	"go/format"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

//go:embed testdata/accountbinding/transition_catalog_creation_test.go.tmpl
var accountCatalogCreationConsumer string

func accountCatalogCreationCases(strategy string) []string {
	result := []string{
		"lifecycle", "shared_group", "shared_permission", "page_boundary", "explicit_empty", "signed_keys", "factory", "configuration", "owned_input", "owned_manifest",
		"duplicate_source", "missing_source", "source_revision", "catalog_limit", "protected_code", "protected_revision", "target_key", "collection_input", "invalid_label", "duplicate_label",
		"target_group_present", "target_permission_present", "source_catalog_changed", "source_added", "source_deleted", "source_account_changed", "target_added", "permission_revoked", "missing_catalog_grant", "additional_catalog_grant", "policy_changed",
		"permission_insert_failure", "group_insert_failure", "grant_insert_failure", "account_link_failure", "audit_failure", "mapping_failure", "receipt_failure", "rollback", "late_catalog_change", "late_catalog_grant", "unowned_incoming", "late_account_change", "late_policy_change", "final_policy_receipt_change",
		"host_references", "host_write_failure", "host_late_change", "final_policy_host_change", "schema_lifecycle", "unknown_commit", "unknown_rollback", "read_cleanup", "canceled", "concurrent", "reconcile_after_edit", "stored_mapping_rewrite",
	}
	if strategy == "staged" {
		result = append(result, "target_account_changed")
	} else {
		result = append(result, "account_insert_failure")
	}
	return result
}

func TestGeneratedAccountCatalogCreation(t *testing.T) {
	root := writeAccountCatalogCreationConsumer(t)
	required := []string{"TestAccountCatalogCreation", "TestAccountCatalogCreationToDefault"}
	for _, strategy := range []string{"staged", "created"} {
		parent := "TestAccountCatalogCreation/" + strategy
		required = append(required, parent)
		for _, name := range accountCatalogCreationCases(strategy) {
			required = append(required, parent+"/"+name)
		}
	}
	runAccountConsumerDatabases(t, root, required, "TestAccountCatalogCreation", "TestAccountCatalogCreationToDefault")
}

func writeAccountCatalogCreationConsumer(t *testing.T) string {
	t.Helper()
	root := writeAccountCatalogTransitionConsumer(t)
	spec, role := accountCatalogTransitionSpec(t)
	baseSpec := spec
	spec.Account = &role
	spec.Project = codegen.PackageSpec{PackageName: "catalogtarget", ImportPath: "example.com/godj-project-bundle/catalogtarget", Directory: "catalogtarget"}
	// The second host imports the exact app types already generated above.
	// Its own project snapshot changes with the selected role; imported app
	// schema/ABI markers still bind both hosts to the same complete model source.
	spec.Apps = slices.Clone(spec.Apps)
	for i := range spec.Apps {
		spec.Apps[i].External = true
		spec.Apps[i].Package.Directory = ""
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range bundle.Files() {
		if strings.HasPrefix(file.Path, "catalogtarget/") {
			writeGeneratedTestFile(t, root, file.Path, file.Source())
		} else {
			t.Fatal("selected factory attempted to own an imported app file", file.Path)
		}
	}
	cases := func(strategy string) string {
		var values []string
		for _, name := range accountCatalogCreationCases(strategy) {
			values = append(values, strconv.Quote(name))
		}
		return strings.Join(values, ",")
	}
	legacy, contracted := accountCatalogReferenceSchemaVariant(baseSpec, "legacy"), accountCatalogReferenceSchemaVariant(baseSpec, "contracted")
	contracted.Account = &role
	stages := accountTransitionMigrationStages(t, legacy, baseSpec, contracted)
	source := strings.NewReplacer("@@STAGED_CASES@@", cases("staged"), "@@CREATED_CASES@@", cases("created"), "@@MIGRATIONS@@", accountConsumerMigrationSources(t, baseSpec), "@@LEGACY_MIGRATIONS@@", stages[0], "@@EXPANDED_MIGRATIONS@@", stages[1]).Replace(accountCatalogCreationConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/transition_catalog_creation_test.go", formatted)
	return root
}
