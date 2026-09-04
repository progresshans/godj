//go:build darwin || linux

package projectcheck

type makemigrationsMode uint8

const (
	makemigrationsModeNormal makemigrationsMode = iota
	makemigrationsModeDryRun
	makemigrationsModeCheck
)

type makemigrationsArguments struct {
	mode               makemigrationsMode
	explicitDescriptor string
}

func parseMakemigrationsArguments(argv []string) (makemigrationsArguments, bool) {
	switch {
	case len(argv) == 1 && argv[0] == "makemigrations":
		return makemigrationsArguments{mode: makemigrationsModeNormal}, true
	case len(argv) == 2 && argv[0] == "makemigrations" && argv[1] == "--dry-run":
		return makemigrationsArguments{mode: makemigrationsModeDryRun}, true
	case len(argv) == 2 && argv[0] == "makemigrations" && argv[1] == "--check":
		return makemigrationsArguments{mode: makemigrationsModeCheck}, true
	case len(argv) == 3 && argv[0] == "makemigrations" && argv[1] == "--project" && argv[2] != "":
		return makemigrationsArguments{
			mode:               makemigrationsModeNormal,
			explicitDescriptor: argv[2],
		}, true
	case len(argv) == 4 && argv[0] == "makemigrations" && argv[1] == "--dry-run" && argv[2] == "--project" && argv[3] != "":
		return makemigrationsArguments{
			mode:               makemigrationsModeDryRun,
			explicitDescriptor: argv[3],
		}, true
	case len(argv) == 4 && argv[0] == "makemigrations" && argv[1] == "--check" && argv[2] == "--project" && argv[3] != "":
		return makemigrationsArguments{
			mode:               makemigrationsModeCheck,
			explicitDescriptor: argv[3],
		}, true
	default:
		return makemigrationsArguments{}, false
	}
}
