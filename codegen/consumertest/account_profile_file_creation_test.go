package codegen_test

import (
	_ "embed"
	"testing"
)

//go:embed testdata/accountbinding/profile_file_creation_test.go.tmpl
var profileFileCreationConsumer string

//go:embed testdata/accountbinding/profile_file_command.go.tmpl
var profileFileCommandConsumer string

func TestGeneratedAccountProfileFileCreation(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			root := writeAccountProfileFileSource(t, custom, true, profileFileCreationConsumer+profileFileCommandConsumer)
			required := []string{"TestGeneratedProfileFileCreation"}
			for _, child := range []string{"command", "construction", "first_account", "first_failures", "typed_creation", "admin_creation", "json_read_only", "failures", "concurrent_login", "restart"} {
				required = append(required, required[0]+"/"+child)
			}
			for _, child := range []string{"construction", "preflight", "created", "mutated_input", "abandon", "not_fresh", "partial", "validation", "hash", "audit", "cleanup", "unknown", "canceled", "report_error", "report_panic", "report_goexit"} {
				required = append(required, required[0]+"/command/"+child)
			}
			for _, child := range []string{"foreign", "reference", "wrong_kind", "file_bytes", "file_total", "filename", "required", "image", "empty"} {
				required = append(required, required[0]+"/command/preflight/"+child)
			}
			for _, child := range []string{"invalid", "read_cleanup", "partial", "validation", "hash", "audit", "cleanup", "unknown_commit", "competing_first"} {
				required = append(required, required[0]+"/first_failures/"+child)
			}
			for _, child := range []string{"invalid", "denied", "read_cleanup", "not_published", "published_cleanup", "uncertain", "partial", "unique", "validator", "password_policy", "hash", "revoked_after_publication", "revoked_after_hash", "audit", "write_cleanup", "unknown_commit", "canceled_after_publication", "reporter"} {
				required = append(required, required[0]+"/failures/"+child)
			}
			runAccountConsumerDatabases(t, root, required)
		})
	}
}
