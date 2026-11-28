//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"reflect"
	"testing"
)

func TestManagementArgumentsSeparateSelectionFromBusinessValues(t *testing.T) {
	for _, test := range []struct {
		args       []string
		descriptor string
		child      []string
	}{
		{[]string{"commands"}, "", []string{"list"}},
		{[]string{"check", "--project", "../site/godj.toml"}, "../site/godj.toml", []string{"check"}},
		{[]string{"check", "--list"}, "", []string{"check", "--list"}},
		{[]string{"check", "--project", "../site/godj.toml", "--resource", "database", "--resource", "storage"}, "../site/godj.toml", []string{"check", "--resource", "database", "--resource", "storage"}},
		{[]string{"help", "--project", "site.toml", "count"}, "site.toml", []string{"help", "count"}},
		{[]string{"command", "--project", "site.toml", "send", "--project", "business-value"}, "site.toml", []string{"run", "send", "--project", "business-value"}},
	} {
		before := append([]string(nil), test.args...)
		descriptor, child, ok := parseManagement(test.args)
		if !ok || descriptor != test.descriptor || !reflect.DeepEqual(child, test.child) || !reflect.DeepEqual(before, test.args) {
			t.Fatal(descriptor, child, ok)
		}
	}
}

func TestManagementRejectsMalformedInputBeforeFilesystemOrBackend(t *testing.T) {
	for _, args := range [][]string{nil, {"commands", "private-extra"}, {"check", "--database"}, {"check", "--resource"}, {"check", "--resource", "private/name"}, {"check", "--resource", "database", "--resource", "database"}, {"check", "--list", "--resource", "database"}, {"command"}, {"command", "--project"}, {"command", "--project", "", "send"}, {"command", "__private"}, {"help", "invalid/name"}, {"help", "send", "extra"}} {
		backend := &scriptedBackend{}
		var stdout, stderr bytes.Buffer
		report := RunManagement(ManagementInvocation{Context: t.Context(), CWD: "/missing/private/project", Args: args, Environment: []string{"PRIVATE=secret"}, Stdout: &stdout, Stderr: &stderr, Backend: backend})
		if report.ExitCode != 2 || report.CommandStarted || len(backend.commands) != 0 || stdout.Len() != 0 || stderr.String() != "project_command_error/invalid_arguments\n" {
			t.Fatal("invalid input reached project work", report, stderr.String())
		}
	}
}

func TestCommandsDescriptorKeepsDeclarationAndRuntimeDistinct(t *testing.T) {
	for _, optional := range []string{
		"commands_package = \"./cmd/commands\"\n",
		"runserver_package = \"./cmd/site\"\ncommands_package = \"./cmd/commands\"\n",
		"commands_package = \"./cmd/site\"\nrunserver_package = \"./cmd/site\"\n",
	} {
		d, err := parseProjectDescriptor([]byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n" + optional))
		if err != nil || d.commandsPackagePath == "" || d.commandsPackagePath == d.packagePath {
			t.Fatal("valid runtime descriptor rejected", err)
		}
	}
	for _, optional := range []string{"commands_package = \"./cmd/projectrunner\"\n", "commands_package = \"../outside\"\n", "commands_package = \"./cmd/a.go\"\n", "commands_package = \"./cmd/site\"\ncommands_package = \"./cmd/other\"\n"} {
		if _, err := parseProjectDescriptor([]byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n" + optional)); err == nil {
			t.Fatal("invalid descriptor accepted")
		}
	}
}
