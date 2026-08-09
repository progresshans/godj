//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

type commandArguments struct {
	explicitDescriptor string
}

type projectDescriptor struct {
	formatVersion uint16
	packagePath   string
}

func parseArguments(argv []string) (commandArguments, *Failure) {
	if len(argv) == 2 && argv[0] == "migrations" && argv[1] == "check" {
		return commandArguments{}, nil
	}
	if len(argv) == 4 && argv[0] == "migrations" && argv[1] == "check" && argv[2] == "--project" && argv[3] != "" {
		return commandArguments{explicitDescriptor: argv[3]}, nil
	}
	primary := failure("migration_project_command_error", "invalid_arguments")
	return commandArguments{}, &primary
}

func parseProjectDescriptor(document []byte) (projectDescriptor, *Failure) {
	invalid := func() (projectDescriptor, *Failure) {
		primary := failure("migration_project_selection_error", "invalid_project_descriptor")
		return projectDescriptor{}, &primary
	}
	if len(document) == 0 || len(document) > maxDescriptorBytes || !utf8.Valid(document) || bytes.HasPrefix(document, []byte{0xef, 0xbb, 0xbf}) {
		return invalid()
	}
	lines, ok := descriptorLines(document)
	if !ok {
		return invalid()
	}
	semantic := make([]string, 0, 3)
	for _, line := range lines {
		trimmed := strings.Trim(line, " \t")
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if !validComment(trimmed[1:]) {
				return invalid()
			}
			continue
		}
		semantic = append(semantic, line)
	}
	if len(semantic) != 3 || strings.Trim(semantic[1], " \t") != "[project]" {
		return invalid()
	}
	versionText, ok := descriptorAssignment(semantic[0], "format_version")
	if !ok || !canonicalDecimal(versionText) {
		return invalid()
	}
	version, err := strconv.ParseUint(versionText, 10, 16)
	if err != nil {
		return invalid()
	}
	packageText, ok := descriptorAssignment(semantic[2], "package")
	if !ok || len(packageText) < 2 || packageText[0] != '"' || packageText[len(packageText)-1] != '"' {
		return invalid()
	}
	packageValue := packageText[1 : len(packageText)-1]
	if !validProjectPackage(packageValue) {
		return invalid()
	}
	if version != 1 {
		primary := failure("migration_project_selection_error", "project_descriptor_incompatible")
		return projectDescriptor{}, &primary
	}
	return projectDescriptor{formatVersion: uint16(version), packagePath: packageValue}, nil
}

func descriptorLines(document []byte) ([]string, bool) {
	if document[len(document)-1] != '\n' {
		return nil, false
	}
	crlf := bytes.Contains(document, []byte("\r\n"))
	if crlf {
		for i := 0; i < len(document); i++ {
			switch document[i] {
			case '\r':
				if i+1 >= len(document) || document[i+1] != '\n' {
					return nil, false
				}
			case '\n':
				if i == 0 || document[i-1] != '\r' {
					return nil, false
				}
			}
		}
		document = bytes.ReplaceAll(document, []byte("\r\n"), []byte("\n"))
	} else if bytes.IndexByte(document, '\r') >= 0 {
		return nil, false
	}
	parts := strings.Split(string(document), "\n")
	return parts[:len(parts)-1], true
}

func validComment(comment string) bool {
	for i := 0; i < len(comment); i++ {
		if comment[i] != '\t' && (comment[i] < 0x20 || comment[i] > 0x7e) {
			return false
		}
	}
	return true
}

func descriptorAssignment(line, key string) (string, bool) {
	line = strings.Trim(line, " \t")
	if !strings.HasPrefix(line, key) {
		return "", false
	}
	rest := line[len(key):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '=' {
		return "", false
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" || rest[0] != '=' {
		return "", false
	}
	rest = strings.TrimLeft(rest[1:], " \t")
	if rest == "" || rest != strings.TrimRight(rest, " \t") {
		return "", false
	}
	return rest, true
}

func canonicalDecimal(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func validProjectPackage(value string) bool {
	if !strings.HasPrefix(value, "./") || len(value) == 2 {
		return false
	}
	remainder := value[2:]
	if path.Clean(remainder) != remainder || strings.ContainsAny(remainder, "\\\x00*?[]{}") {
		return false
	}
	for i := 0; i < len(remainder); i++ {
		if remainder[i] < 0x21 || remainder[i] > 0x7e || remainder[i] == '"' {
			return false
		}
	}
	for _, segment := range strings.Split(remainder, "/") {
		if segment == "" || segment == "." || segment == ".." || segment == "..." {
			return false
		}
	}
	return true
}
