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
	if len(argv) < 3 || len(argv) > 6 || argv[0] != "sqlmigrate" || !validSQLMigrateToken(argv[1]) || !validSQLMigrateToken(argv[2]) {
		return invalidSQLMigrateArguments()
	}
	arguments.request = sqlmigrateprotocol.Request{App: argv[1], Name: argv[2]}
	for i := 3; i < len(argv); i++ {
		switch argv[i] {
		case "--backwards":
			if arguments.request.Backwards {
				return invalidSQLMigrateArguments()
			}
			arguments.request.Backwards = true
		case "--project":
			i++
			if arguments.explicitDescriptor != "" || i == len(argv) || !validSQLMigrateToken(argv[i]) {
				return invalidSQLMigrateArguments()
			}
			arguments.explicitDescriptor = argv[i]
		default:
			return invalidSQLMigrateArguments()
		}
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

func invalidSQLMigrateArguments() (sqlMigrateArguments, *SQLMigrateFailure) {
	failure := SQLMigrateFailure{Category: sqlmigrateprotocol.CategoryCommand, Code: sqlmigrateprotocol.CodeInvalidArguments}
	return sqlMigrateArguments{}, &failure
}
