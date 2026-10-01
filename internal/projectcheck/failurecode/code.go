// Package failurecode owns shared closed CLI failure-code sets. Protocols keep
// category names, command-specific codes, private-wire eligibility and results.
package failurecode

func Exact(code string, exit int, allowed ...string) (int, bool) {
	for _, candidate := range allowed {
		if code == candidate {
			return exit, true
		}
	}
	return 0, false
}

func Selection(code string, bindingFailureExit int) (int, bool) {
	if code == "project_selection_failed" {
		return bindingFailureExit, true
	}
	return Exact(code, 2, "project_not_found", "project_search_limit_exceeded", "invalid_project_descriptor", "project_descriptor_incompatible")
}

func Process(code string) (int, bool) {
	if code == "project_interrupted" {
		return 130, true
	}
	return Exact(code, 3, "project_canceled", "project_cleanup_failed")
}

func Discovery(code string) (int, bool) {
	switch code {
	case "invalid_project_source_config", "invalid_source_root":
		return 2, true
	case "invalid_source_entry", "unsafe_source_entry", "source_catalog_limit_exceeded":
		return 1, true
	case "source_discovery_failed", "source_read_failed":
		return 3, true
	}
	return 0, false
}

func Source(code string) (int, bool) {
	return Exact(code, 1, "invalid_definition_source", "invalid_definition_document", "definition_format_incompatible", "unsupported_definition_operation", "invalid_definition_operation", "invalid_definition_ir")
}

func Graph(code string) (int, bool) {
	return Exact(code, 1, "invalid_node", "duplicate_node", "invalid_dependency", "duplicate_dependency", "dependency_not_found", "dependency_cycle")
}
