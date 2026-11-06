//go:build darwin || linux

package projectcheck

import (
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
)

type sqlMigrateArguments struct {
	explicitDescriptor string
	request            sqlmigrateprotocol.Request
	requestDocument    []byte
}

func parseSQLMigrateArguments(argv []string) (sqlMigrateArguments, *SQLMigrateFailure) {
	var arguments sqlMigrateArguments
	valid := false
	switch {
	case len(argv) == 3 && argv[0] == "sqlmigrate" && validSQLMigrateToken(argv[1]) && validSQLMigrateToken(argv[2]):
		arguments.request = sqlmigrateprotocol.Request{App: argv[1], Name: argv[2]}
		valid = true
	case len(argv) == 5 && argv[0] == "sqlmigrate" && validSQLMigrateToken(argv[1]) && validSQLMigrateToken(argv[2]) &&
		argv[3] == "--project" && validSQLMigrateDescriptorToken(argv[4]):
		arguments.request = sqlmigrateprotocol.Request{App: argv[1], Name: argv[2]}
		arguments.explicitDescriptor = argv[4]
		valid = true
	}
	if !valid {
		return invalidSQLMigrateArguments()
	}
	document, err := sqlmigrateprotocol.EncodeRequest(arguments.request)
	if err != nil {
		return invalidSQLMigrateArguments()
	}
	arguments.requestDocument = document
	return arguments, nil
}

func validSQLMigrateToken(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.HasPrefix(value, "-")
}

func validSQLMigrateDescriptorToken(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.HasPrefix(value, "-")
}

func invalidSQLMigrateArguments() (sqlMigrateArguments, *SQLMigrateFailure) {
	failure := SQLMigrateFailure{Category: sqlmigrateprotocol.CategoryCommand, Code: sqlmigrateprotocol.CodeInvalidArguments}
	return sqlMigrateArguments{}, &failure
}
