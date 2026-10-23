// Package compatibility identifies the framework contracts linked into an
// executable without loading settings, declarations, apps, or a database.
package compatibility

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	generateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	migrationprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/version"
)

const PrivateArgument = "__godj_compatibility_v1"
const MaxBytes = 16 << 10
const CommandVersion uint64 = 1

type Contract struct {
	Schema          int    `json:"schema"`
	Bundle          int    `json:"bundle"`
	Generator       string `json:"generator"`
	MigrationCheck  uint64 `json:"migration_check"`
	Generate        uint64 `json:"generate"`
	MakeMigrations  uint64 `json:"makemigrations"`
	Migrate         uint64 `json:"migrate"`
	ShowMigrations  uint64 `json:"showmigrations"`
	SQLMigrate      uint64 `json:"sqlmigrate"`
	CreateSuperuser uint64 `json:"createsuperuser"`
	Commands        uint64 `json:"commands"`
}

type Info struct {
	FormatVersion int          `json:"format_version"`
	Framework     version.Info `json:"framework"`
	Contract      Contract     `json:"contract"`
}

func Current() Info {
	return Info{FormatVersion: 1, Framework: version.Current(), Contract: Contract{
		Schema: ir.CurrentFormatVersion, Bundle: codegen.ProjectBundleFormatVersion, Generator: codegen.ProjectGeneratorABI(),
		MigrationCheck: protocol.Version, Generate: generateprotocol.Version, MakeMigrations: migrationprotocol.Version,
		Migrate: migrateprotocol.Version, ShowMigrations: showmigrationsprotocol.Version, SQLMigrate: sqlmigrateprotocol.Version,
		CreateSuperuser: createsuperuserprotocol.Version, Commands: CommandVersion,
	}}
}

// Serve handles only the exact metadata probe. Entry points call it before
// configuration/resource initialization; handled=false leaves their argv alone.
func Serve(ctx context.Context, argv []string, stdout io.Writer) (handled bool, err error) {
	if len(argv) != 1 || argv[0] != PrivateArgument {
		return false, nil
	}
	if ctx == nil || stdout == nil {
		return true, errors.New("compatibility: invalid invocation")
	}
	if ctx.Err() != nil {
		return true, errors.New("compatibility: canceled")
	}
	data, err := json.Marshal(Current())
	if err != nil {
		return true, errors.New("compatibility: encode failed")
	}
	data = append(data, '\n')
	n, err := stdout.Write(data)
	if err != nil || n != len(data) {
		return true, errors.New("compatibility: output failed")
	}
	return true, nil
}

func Parse(document []byte) (Info, error) {
	// Serve and json.Encoder terminate one canonical object with one LF.
	// Only that framing byte is optional; arbitrary whitespace and extra
	// documents still fail the canonical decoder below.
	if len(document) > MaxBytes {
		return Info{}, errors.New("compatibility: invalid response")
	}
	document = bytes.TrimSuffix(document, []byte{'\n'})
	if err := wirejson.Scan(document, wirejson.Limits{Bytes: MaxBytes, Containers: 4, Values: 64, ObjectKeys: 32, StringBytes: 1024, KeyBytes: 64, RejectArrays: true, RejectNull: true}); err != nil {
		return Info{}, errors.New("compatibility: invalid response")
	}
	var info Info
	if wirejson.DecodeCanonical(document, &info) != nil || !valid(info) {
		return Info{}, errors.New("compatibility: invalid response")
	}
	return info, nil
}

// Check requires equal contracts and, for versioned modules, equal effective
// module identities. Two explicitly identified development builds may use the
// same contracts; that does not attest that their source bytes are identical.
// Unknown identities and mixed development/versioned pairs fail closed.
func Check(tool, linked Info) error {
	if !valid(tool) || !valid(linked) {
		return errors.New("compatibility: invalid identity")
	}
	if tool.FormatVersion != linked.FormatVersion || tool.Contract != linked.Contract {
		return errors.New("compatibility: contract mismatch")
	}
	toolModule, toolVersion := effective(tool.Framework)
	linkedModule, linkedVersion := effective(linked.Framework)
	if toolModule != linkedModule || toolVersion != linkedVersion {
		return errors.New("compatibility: framework version mismatch")
	}
	return nil
}

func effective(info version.Info) (string, string) {
	if info.Origin == version.Development || info.Origin == version.LocalReplacement {
		return version.Module, "development"
	}
	if info.Origin == version.ModuleReplacement {
		return info.ReplacementModule, info.Version
	}
	return info.Module, info.Version
}

func valid(info Info) bool {
	if info.FormatVersion != 1 || info.Framework.Module != version.Module || info.Contract.Schema <= 0 || info.Contract.Bundle <= 0 {
		return false
	}
	if len(info.Contract.Generator) != 64 || strings.Trim(info.Contract.Generator, "0123456789abcdef") != "" {
		return false
	}
	for _, v := range []uint64{info.Contract.MigrationCheck, info.Contract.Generate, info.Contract.MakeMigrations, info.Contract.Migrate, info.Contract.ShowMigrations, info.Contract.SQLMigrate, info.Contract.CreateSuperuser, info.Contract.Commands} {
		if v == 0 || v > 65535 {
			return false
		}
	}
	f := info.Framework
	for _, token := range []string{f.Version, f.RequestedVersion, f.GoVersion, f.Revision} {
		if len(token) > 256 || strings.Trim(token, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-+") != "" {
			return false
		}
	}
	if f.RequestedVersion != "" && !versionToken(f.RequestedVersion) {
		return false
	}
	switch f.Origin {
	case version.Development:
		return (f.Version == "" || versionToken(f.Version)) && f.RequestedVersion == "" && f.ReplacementModule == ""
	case version.LocalReplacement:
		return f.Version == "" && f.ReplacementModule == "" && f.Revision == "" && !f.Modified
	case version.ModuleVersion:
		return versionToken(f.Version) && f.RequestedVersion == "" && f.ReplacementModule == ""
	case version.ModuleReplacement:
		return versionToken(f.Version) && f.RequestedVersion != "" && validModule(f.ReplacementModule) && f.Revision == "" && !f.Modified
	}
	return false
}

func versionToken(value string) bool {
	return len(value) >= 2 && value[0] == 'v' && value[1] >= '0' && value[1] <= '9'
}

func validModule(value string) bool {
	if len(value) == 0 || len(value) > 1024 || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.Trim(part, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-_~") != "" {
			return false
		}
	}
	return true
}
