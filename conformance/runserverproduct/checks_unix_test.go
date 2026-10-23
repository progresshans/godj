//go:build darwin || linux

package runserverproduct_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/testenv"
)

func exerciseStartedChecksBeforeDatabase(t *testing.T, global, project, workspace string, environment []string) {
	t.Helper()
	before := snapshotRunserverProjectTree(t, project)
	run := func(env []string, args ...string) testprocess.CommandResult {
		t.Helper()
		result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, project, env, global, args...)
		assertRunserverWorkspaceEmpty(t, workspace)
		return result
	}
	listed := run(environment, "check", "--list")
	if listed.ExitCode != 0 || listed.Stderr != "" || !strings.HasPrefix(listed.Stdout, "database\t") {
		t.Fatal("explicit check inventory missing", listed.ExitCode, listed.Stderr)
	}
	checked := run(environment, "check", "--resource", "database")
	if checked.ExitCode != 1 || checked.Stdout != "" || checked.Stderr != "command_error/resource_check_failed/database/database\n" {
		t.Fatal("missing database check did not fail closed", checked.ExitCode, checked.Stderr)
	}
	unknown := run(environment, "check", "--resource", "database", "--resource", "missing")
	if unknown.ExitCode != 2 || unknown.Stdout != "" || unknown.Stderr != "command_error/unknown_check\n" {
		t.Fatal("incomplete selection reached I/O", unknown.ExitCode, unknown.Stderr)
	}
	for _, test := range []struct {
		values map[string]string
		field  string
	}{
		{map[string]string{"GODJ_SQLITE_DATABASE": ""}, "GODJ_SQLITE_DATABASE"},
		{map[string]string{"GODJ_POSTGRES_SCHEMA": "private-schema-value"}, "GODJ_POSTGRES_URL"},
		{map[string]string{"GODJ_SQLITE_DATABASE": "private-path-value", "GODJ_POSTGRES_URL": "postgres://private-user:private-password@localhost/private-db", "GODJ_POSTGRES_SCHEMA": "private_schema"}, "database"},
	} {
		result := run(testenv.With(environment, test.values), "check", "--resource", "database")
		if result.ExitCode != 1 || result.Stdout != "" || result.Stderr != "command_error/check_failed/"+test.field+"\n" {
			t.Fatal("invalid configuration lost its public field or exposed a value", result.ExitCode)
		}
	}
	if !reflect.DeepEqual(before, snapshotRunserverProjectTree(t, project)) {
		t.Fatal("check discovery, missing database or invalid configuration changed project files")
	}
}

func assertStartedDatabaseCheckPreservesTree(t *testing.T, global, project, workspace string, environment []string) {
	t.Helper()
	before := snapshotRunserverProjectTree(t, project)
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, project, environment, global, "check", "--resource", "database")
	assertRunserverWorkspaceEmpty(t, workspace)
	if result.ExitCode != 0 || result.Stdout != "" || result.Stderr != "" {
		t.Fatal("selected existing database check failed", result.ExitCode, result.Stderr)
	}
	if !reflect.DeepEqual(before, snapshotRunserverProjectTree(t, project)) {
		t.Fatal("selected database check changed source or stored data")
	}
}
