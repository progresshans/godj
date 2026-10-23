//go:build darwin || linux

package projectcheck

import (
	"reflect"
	"testing"
)

func TestParseMakemigrationsArgumentsExactForms(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name string
		argv []string
		want makemigrationsArguments
	}{
		{
			name: "normal",
			argv: []string{"makemigrations"},
			want: makemigrationsArguments{mode: makemigrationsModeNormal},
		},
		{
			name: "check",
			argv: []string{"makemigrations", "--check"},
			want: makemigrationsArguments{mode: makemigrationsModeCheck},
		},
		{
			name: "dry run",
			argv: []string{"makemigrations", "--dry-run"},
			want: makemigrationsArguments{mode: makemigrationsModeDryRun},
		},
		{
			name: "normal with project",
			argv: []string{"makemigrations", "--project", "godj.toml"},
			want: makemigrationsArguments{
				mode:               makemigrationsModeNormal,
				explicitDescriptor: "godj.toml",
			},
		},
		{
			name: "check with project",
			argv: []string{"makemigrations", "--check", "--project", "godj.toml"},
			want: makemigrationsArguments{
				mode:               makemigrationsModeCheck,
				explicitDescriptor: "godj.toml",
			},
		},
		{
			name: "dry run with project",
			argv: []string{"makemigrations", "--dry-run", "--project", "godj.toml"},
			want: makemigrationsArguments{
				mode:               makemigrationsModeDryRun,
				explicitDescriptor: "godj.toml",
			},
		},
	}

	for _, test := range valid {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseMakemigrationsArguments(test.argv)
			if !ok {
				t.Fatal("parseMakemigrationsArguments() rejected a valid argv")
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseMakemigrationsArguments() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseMakemigrationsArgumentsRejectsOtherForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		argv []string
	}{
		{name: "nil", argv: nil},
		{name: "empty", argv: []string{}},
		{name: "wrong command", argv: []string{"generate"}},
		{name: "empty argument", argv: []string{"makemigrations", ""}},
		{name: "project missing value", argv: []string{"makemigrations", "--project"}},
		{name: "project empty value", argv: []string{"makemigrations", "--project", ""}},
		{name: "check project empty value", argv: []string{"makemigrations", "--check", "--project", ""}},
		{name: "dry run project empty value", argv: []string{"makemigrations", "--dry-run", "--project", ""}},
		{name: "project before check", argv: []string{"makemigrations", "--project", "godj.toml", "--check"}},
		{name: "project before dry run", argv: []string{"makemigrations", "--project", "godj.toml", "--dry-run"}},
		{name: "both modes check first", argv: []string{"makemigrations", "--check", "--dry-run"}},
		{name: "both modes dry run first", argv: []string{"makemigrations", "--dry-run", "--check"}},
		{name: "both modes with project", argv: []string{"makemigrations", "--check", "--dry-run", "--project", "godj.toml"}},
		{name: "repeated check", argv: []string{"makemigrations", "--check", "--check"}},
		{name: "repeated dry run", argv: []string{"makemigrations", "--dry-run", "--dry-run"}},
		{name: "repeated project", argv: []string{"makemigrations", "--project", "godj.toml", "--project", "other.toml"}},
		{name: "project equals", argv: []string{"makemigrations", "--project=godj.toml"}},
		{name: "check equals", argv: []string{"makemigrations", "--check=true"}},
		{name: "dry run equals", argv: []string{"makemigrations", "--dry-run=true"}},
		{name: "check with project equals", argv: []string{"makemigrations", "--check", "--project=godj.toml"}},
		{name: "dry run with project equals", argv: []string{"makemigrations", "--dry-run", "--project=godj.toml"}},
		{name: "extra normal argument", argv: []string{"makemigrations", "extra"}},
		{name: "extra check argument", argv: []string{"makemigrations", "--check", "extra"}},
		{name: "extra dry run argument", argv: []string{"makemigrations", "--dry-run", "extra"}},
		{name: "extra project argument", argv: []string{"makemigrations", "--project", "godj.toml", "extra"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseMakemigrationsArguments(test.argv)
			if ok {
				t.Fatalf("parseMakemigrationsArguments() = %#v, true; want rejection", got)
			}
			if got != (makemigrationsArguments{}) {
				t.Fatalf("parseMakemigrationsArguments() rejected with %#v, want zero arguments", got)
			}
		})
	}
}
