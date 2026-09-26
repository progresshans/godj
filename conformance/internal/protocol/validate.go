package protocol

import (
	"fmt"
	"path"
	"reflect"
	"regexp"
	"strings"
)

var (
	contractIDPattern    = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]{3}$`)
	identifierPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
	tokenPattern         = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	hex40Pattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hex64Pattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	decisionPattern      = regexp.MustCompile(`^DEV-[0-9]{4}$`)
	deviationPathPattern = regexp.MustCompile(`^(?:\[[0-9]+\](?:\.[a-z][a-z0-9_]*(?:\[[0-9]+\])?)*|[a-z][a-z0-9_]*(?:\[[0-9]+\])?(?:\.[a-z][a-z0-9_]*(?:\[[0-9]+\])?)*)$`)
)

const queryExpressionScenarioPrefix = "django.query.expression."

func extendedQueryExpressionScenarioRegistry() [20]string {
	return [...]string{
		queryExpressionScenarioPrefix + "scalar_exact_or",
		queryExpressionScenarioPrefix + "escaped_ascii_icontains_or",
		queryExpressionScenarioPrefix + "grouped_or_and_reuse",
		queryExpressionScenarioPrefix + "nonnull_scalar_not",
		queryExpressionScenarioPrefix + "nullable_negation_truth_table",
		queryExpressionScenarioPrefix + "implicit_filter_and",
		queryExpressionScenarioPrefix + "nested_connector_order_and_source_independence",
		queryExpressionScenarioPrefix + "composite_distinct_stable_page",
		queryExpressionScenarioPrefix + "projection_outside_predicate",
		queryExpressionScenarioPrefix + "composite_count_max",
		queryExpressionScenarioPrefix + "integer_gt_literal_boundary",
		queryExpressionScenarioPrefix + "integer_gte_literal_boundary",
		queryExpressionScenarioPrefix + "integer_lt_literal_boundary",
		queryExpressionScenarioPrefix + "integer_lte_literal_boundary",
		queryExpressionScenarioPrefix + "range_composition_negation_and_reuse",
		queryExpressionScenarioPrefix + "same_field_reference_boundaries",
		queryExpressionScenarioPrefix + "same_model_field_reference_and_nullable_negation",
		queryExpressionScenarioPrefix + "nullable_ordering_negation_truth_table",
		queryExpressionScenarioPrefix + "field_reference_stable_projection",
		queryExpressionScenarioPrefix + "field_reference_count_max",
	}
}

func extendedSystemStateScenarioRegistry() [30]string {
	return [...]string{
		"godj.system_state.explicit_migration_gate",
		"godj.system_state.admin_bootstrap_gate",
		"django.system_state.credential_permission_restart",
		"django.system_state.rotated_session_restart",
		"godj.system_state.session_expiry_and_touch",
		"godj.system_state.capacity_reap_and_rotate_rollback",
		"godj.system_state.digest_only_current_codec",
		"django.system_state.logout_restart_denial",
		"django.system_state.csrf_restart",
		"django.system_state.admin_audit_fault_rollback",
		"django.system_state.audit_history_restart",
		"godj.system_state.commit_outcome_unknown",
		"godj.system_state.coordinated_atomic_fence",
		"godj.system_state.concurrent_admin_bootstrap",
		"godj.system_state.concurrent_session_capacity",
		"godj.system_state.concurrent_touch_monotonicity",
		"godj.system_state.concurrent_session_rotation",
		"godj.system_state.concurrent_article_audit",
		"godj.system_state.shared_csrf_key_ring",
		"godj.system_state.two_process_backend_restart",
		"godj.system_state.explicit_operator_provisioning",
		"godj.system_state.createsuperuser_argv_and_pre_io",
		"godj.system_state.tty_secret_transport",
		"godj.system_state.project_provision_ownership",
		"godj.system_state.operator_provision_cardinality",
		"godj.system_state.provision_outcome_ownership",
		"godj.system_state.open_existing_authenticator",
		"godj.system_state.credential_absent_public_only",
		"godj.system_state.operator_backend_login_restart",
		"godj.system_state.sensitive_child_cleanup",
	}
}

func (p Profile) Validate() error {
	if p.FormatVersion != FormatVersion {
		return fmt.Errorf("format_version must be %d", FormatVersion)
	}
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if err := p.Fingerprint.Validate(); err != nil {
		return fmt.Errorf("fingerprint: %w", err)
	}
	if err := p.Lock.Validate(); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	return nil
}

func (p ProfileSnapshot) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if err := p.Fingerprint.Validate(); err != nil {
		return fmt.Errorf("fingerprint: %w", err)
	}
	if err := p.Lock.Validate(); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	return nil
}

func (p ProfileFingerprint) Validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"django_version", p.DjangoVersion},
		{"django_commit", p.DjangoCommit},
		{"django_distribution_sha256", p.DjangoDistributionSHA256},
		{"python_implementation", p.PythonImplementation},
		{"python_version", p.PythonVersion},
		{"sqlite_version", p.SQLiteVersion},
		{"sqlite_source_id", p.SQLiteSourceID},
		{"database_engine", p.DatabaseEngine},
		{"timezone", p.Timezone},
		{"language_code", p.LanguageCode},
		{"locale", p.Locale},
		{"platform", p.Platform},
		{"architecture", p.Architecture},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if !hex40Pattern.MatchString(p.DjangoCommit) {
		return fmt.Errorf("django_commit must be 40 lowercase hexadecimal characters")
	}
	if !hex64Pattern.MatchString(p.DjangoDistributionSHA256) {
		return fmt.Errorf("django_distribution_sha256 must be 64 lowercase hexadecimal characters")
	}
	if p.UseTZ == nil {
		return fmt.Errorf("use_tz is required")
	}
	return nil
}

func (l LockMetadata) Validate() error {
	if strings.TrimSpace(l.File) == "" {
		return fmt.Errorf("file is required")
	}
	if path.IsAbs(l.File) || path.Clean(l.File) != l.File || l.File == "." || strings.HasPrefix(l.File, "../") {
		return fmt.Errorf("file must be a clean repository-relative slash path")
	}
	if !hex64Pattern.MatchString(l.SHA256) {
		return fmt.Errorf("sha256 must be 64 lowercase hexadecimal characters")
	}
	if strings.TrimSpace(l.Manager) == "" {
		return fmt.Errorf("manager is required")
	}
	if strings.TrimSpace(l.ManagerVersion) == "" {
		return fmt.Errorf("manager_version is required")
	}
	return nil
}

func (m Manifest) Validate() error {
	if m.FormatVersion != FormatVersion {
		return fmt.Errorf("format_version must be %d", FormatVersion)
	}
	if strings.TrimSpace(m.ProfileID) == "" {
		return fmt.Errorf("profile_id is required")
	}
	contractCount := len(m.Contracts)
	if contractCount < 8 || (contractCount > 12 && !manifestHasExactExtendedQueryExpressionRegistry(m.Contracts) && !manifestHasExactExtendedSystemStateRegistry(m.Contracts)) {
		return fmt.Errorf("contracts must contain 8 to 12 ordered entries, the exact 20-entry query-expression registry, or the exact 30-entry system-state registry, got %d", contractCount)
	}
	seen := make(map[string]struct{}, len(m.Contracts))
	for index := range m.Contracts {
		contract := m.Contracts[index]
		if err := contract.Validate(); err != nil {
			return fmt.Errorf("contract %d: %w", index, err)
		}
		if _, exists := seen[contract.ID]; exists {
			return fmt.Errorf("contract %d: duplicate id %q", index, contract.ID)
		}
		seen[contract.ID] = struct{}{}
	}
	return nil
}

func manifestHasExactExtendedQueryExpressionRegistry(contracts []Contract) bool {
	scenarios := extendedQueryExpressionScenarioRegistry()
	if len(contracts) != len(scenarios) {
		return false
	}
	for index, scenario := range scenarios {
		if contracts[index].ID != fmt.Sprintf("QRY-%03d", index+34) || contracts[index].Scenario != scenario {
			return false
		}
	}
	return true
}

func manifestHasExactExtendedSystemStateRegistry(contracts []Contract) bool {
	scenarios := extendedSystemStateScenarioRegistry()
	if len(contracts) != len(scenarios) {
		return false
	}
	for index, scenario := range scenarios {
		if contracts[index].ID != fmt.Sprintf("SYS-%03d", index+1) || contracts[index].Scenario != scenario {
			return false
		}
	}
	return true
}

func (c Contract) Validate() error {
	if !contractIDPattern.MatchString(c.ID) {
		return fmt.Errorf("id %q must match %s", c.ID, contractIDPattern)
	}
	if strings.TrimSpace(c.Title) == "" {
		return fmt.Errorf("%s: title is required", c.ID)
	}
	if !identifierPattern.MatchString(c.Scenario) {
		return fmt.Errorf("%s: scenario %q must match %s", c.ID, c.Scenario, identifierPattern)
	}
	if !c.Phase.valid() {
		return fmt.Errorf("%s: unknown phase %q", c.ID, c.Phase)
	}
	switch c.Status {
	case ContractDraft, ContractOracleLocked, ContractRed, ContractPassing, ContractDeviation:
	default:
		return fmt.Errorf("%s: unknown status %q", c.ID, c.Status)
	}
	if len(c.Provenance) == 0 {
		return fmt.Errorf("%s: at least one provenance entry is required", c.ID)
	}
	for index := range c.Provenance {
		if err := c.Provenance[index].Validate(); err != nil {
			return fmt.Errorf("%s: provenance %d: %w", c.ID, index, err)
		}
	}
	if len(c.Comparison) == 0 {
		return fmt.Errorf("%s: at least one comparison dimension is required", c.ID)
	}
	seen := make(map[ComparisonDimension]struct{}, len(c.Comparison))
	for index, dimension := range c.Comparison {
		switch dimension {
		case CompareResult, CompareError, CompareDBState, CompareMetrics:
		default:
			return fmt.Errorf("%s: comparison %d has unknown dimension %q", c.ID, index, dimension)
		}
		if _, exists := seen[dimension]; exists {
			return fmt.Errorf("%s: duplicate comparison dimension %q", c.ID, dimension)
		}
		seen[dimension] = struct{}{}
	}
	return nil
}

func (p Provenance) Validate() error {
	if !tokenPattern.MatchString(p.Kind) {
		return fmt.Errorf("kind %q must be a lowercase token", p.Kind)
	}
	if strings.TrimSpace(p.Reference) == "" {
		return fmt.Errorf("reference is required")
	}
	if p.Derived == nil {
		return fmt.Errorf("derived is required")
	}
	if *p.Derived && strings.TrimSpace(p.License) == "" {
		return fmt.Errorf("license is required for derived provenance")
	}
	return nil
}

func (s ObservationSuite) Validate() error {
	if s.FormatVersion != FormatVersion {
		return fmt.Errorf("format_version must be %d", FormatVersion)
	}
	if err := s.Profile.Validate(); err != nil {
		return fmt.Errorf("profile: %w", err)
	}
	if len(s.Contracts) == 0 {
		return fmt.Errorf("contracts must not be empty")
	}
	seen := make(map[string]struct{}, len(s.Contracts))
	for index := range s.Contracts {
		observation := s.Contracts[index]
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("contract %d: %w", index, err)
		}
		if _, exists := seen[observation.ID]; exists {
			return fmt.Errorf("contract %d: duplicate id %q", index, observation.ID)
		}
		seen[observation.ID] = struct{}{}
	}
	return nil
}

func (o Observation) Validate() error {
	if !contractIDPattern.MatchString(o.ID) {
		return fmt.Errorf("id %q must match %s", o.ID, contractIDPattern)
	}
	switch o.Status {
	case StatusObserved, StatusNotImplemented:
	default:
		return fmt.Errorf("%s: unknown status %q", o.ID, o.Status)
	}
	if !o.Phase.valid() {
		return fmt.Errorf("%s: unknown phase %q", o.ID, o.Phase)
	}
	if o.Status == StatusNotImplemented {
		if o.Result != nil || o.Error != nil || o.DBState != nil || o.Metrics != nil {
			return fmt.Errorf("%s: not_implemented observation cannot contain payloads", o.ID)
		}
		return nil
	}
	if o.Result == nil && o.Error == nil && o.DBState == nil && o.Metrics == nil {
		return fmt.Errorf("%s: observed observation must contain at least one payload", o.ID)
	}
	if o.Result != nil && o.Error != nil {
		return fmt.Errorf("%s: result and error are mutually exclusive", o.ID)
	}
	if o.Result != nil {
		if err := o.Result.Validate(); err != nil {
			return fmt.Errorf("%s: result: %w", o.ID, err)
		}
	}
	if o.Error != nil {
		if err := o.Error.Validate(); err != nil {
			return fmt.Errorf("%s: error: %w", o.ID, err)
		}
	}
	if o.DBState != nil {
		if err := o.DBState.Validate(); err != nil {
			return fmt.Errorf("%s: db_state: %w", o.ID, err)
		}
	}
	if o.Metrics != nil {
		if o.Metrics.Type != ValueObject {
			return fmt.Errorf("%s: metrics must be an object value", o.ID)
		}
		if err := o.Metrics.Validate(); err != nil {
			return fmt.Errorf("%s: metrics: %w", o.ID, err)
		}
	}
	return nil
}

func (e DeviationExpectation) Validate() error {
	if e.FormatVersion != FormatVersion {
		return fmt.Errorf("format_version must be %d", FormatVersion)
	}
	if strings.TrimSpace(e.ProfileID) == "" {
		return fmt.Errorf("profile_id is required")
	}
	if !decisionPattern.MatchString(e.Decision) {
		return fmt.Errorf("decision %q must match %s", e.Decision, decisionPattern)
	}
	if len(e.Contracts) == 0 || len(e.Contracts) > 12 {
		return fmt.Errorf("contracts must contain 1 to 12 ordered deviation entries, got %d", len(e.Contracts))
	}
	seen := make(map[string]struct{}, len(e.Contracts))
	for index := range e.Contracts {
		contract := e.Contracts[index]
		if err := contract.Validate(); err != nil {
			return fmt.Errorf("contract %d: %w", index, err)
		}
		if _, exists := seen[contract.ID]; exists {
			return fmt.Errorf("contract %d: duplicate id %q", index, contract.ID)
		}
		seen[contract.ID] = struct{}{}
	}
	return nil
}

func (e DeviationContractExpectation) Validate() error {
	if !contractIDPattern.MatchString(e.ID) {
		return fmt.Errorf("id %q must match %s", e.ID, contractIDPattern)
	}
	if len(e.Changes) == 0 {
		return fmt.Errorf("%s: changes must not be empty", e.ID)
	}
	seen := make(map[string]struct{}, len(e.Changes))
	for index := range e.Changes {
		change := e.Changes[index]
		if err := change.Validate(); err != nil {
			return fmt.Errorf("%s: change %d: %w", e.ID, index, err)
		}
		key := string(change.Dimension) + "\x00" + change.Path
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%s: change %d duplicates dimension %q path %q", e.ID, index, change.Dimension, change.Path)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (c DeviationChange) Validate() error {
	if err := c.Reference.Validate(); err != nil {
		return fmt.Errorf("reference: %w", err)
	}
	if err := c.Product.Validate(); err != nil {
		return fmt.Errorf("product: %w", err)
	}
	switch c.Dimension {
	case DeviationPhase:
		if c.Path != "" {
			return fmt.Errorf("phase change path must be empty")
		}
		if c.Operation != DeviationReplace {
			return fmt.Errorf("phase change operation must be %q", DeviationReplace)
		}
		if c.Reference.Type != ValueString || c.Product.Type != ValueString {
			return fmt.Errorf("phase reference and product must be string values")
		}
		if !Phase(*c.Reference.Text).valid() || !Phase(*c.Product.Text).valid() {
			return fmt.Errorf("phase reference and product must be known phases")
		}
	case DeviationResult, DeviationDBState, DeviationMetrics:
		if !deviationPathPattern.MatchString(c.Path) {
			return fmt.Errorf("path %q must match %s", c.Path, deviationPathPattern)
		}
		switch c.Operation {
		case DeviationReplace:
		case DeviationInsertBefore:
			if !strings.HasSuffix(c.Path, "]") {
				return fmt.Errorf("insert_before path %q must end at a list index", c.Path)
			}
		default:
			return fmt.Errorf("unknown operation %q", c.Operation)
		}
	default:
		return fmt.Errorf("unknown dimension %q", c.Dimension)
	}
	if reflect.DeepEqual(c.Reference, c.Product) {
		return fmt.Errorf("reference and product must differ")
	}
	return nil
}

func (e ObservedError) Validate() error {
	if !tokenPattern.MatchString(e.Category) {
		return fmt.Errorf("category %q must be a lowercase token", e.Category)
	}
	if !tokenPattern.MatchString(e.Code) {
		return fmt.Errorf("code %q must be a lowercase token", e.Code)
	}
	if e.MessageIsContract == nil {
		return fmt.Errorf("message_is_contract is required")
	}
	if *e.MessageIsContract && e.Message == "" {
		return fmt.Errorf("message is required when message_is_contract is true")
	}
	return nil
}

func ValidateSuiteAgainst(profile Profile, manifest Manifest, suite ObservationSuite) error {
	if err := profile.Validate(); err != nil {
		return fmt.Errorf("profile: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := suite.Validate(); err != nil {
		return fmt.Errorf("suite: %w", err)
	}
	if manifest.ProfileID != profile.ID {
		return fmt.Errorf("manifest profile_id %q does not match profile id %q", manifest.ProfileID, profile.ID)
	}
	if !reflect.DeepEqual(suite.Profile, profile.Snapshot()) {
		return fmt.Errorf("suite profile fingerprint does not match locked profile %q", profile.ID)
	}
	if len(suite.Contracts) != len(manifest.Contracts) {
		return fmt.Errorf("suite contains %d contracts; manifest requires %d", len(suite.Contracts), len(manifest.Contracts))
	}
	for index := range manifest.Contracts {
		contract := manifest.Contracts[index]
		observation := suite.Contracts[index]
		if contract.Status == ContractDraft {
			return fmt.Errorf("%s: observation suite requires a locked-or-later manifest status", contract.ID)
		}
		if observation.ID != contract.ID {
			return fmt.Errorf("suite contract %d is %q; manifest requires %q in that position", index, observation.ID, contract.ID)
		}
		if observation.Phase != contract.Phase {
			return fmt.Errorf("%s: observation phase %q does not match manifest phase %q", contract.ID, observation.Phase, contract.Phase)
		}
		if observation.Status == StatusNotImplemented {
			continue
		}
		if err := validateDimensions(contract, observation); err != nil {
			return err
		}
	}
	return nil
}

func (p Phase) valid() bool {
	switch p {
	case PhaseEnvironment, PhaseMetadata, PhaseConstruction, PhaseEvaluation, PhaseCommit, PhaseRollback:
		return true
	default:
		return false
	}
}

func validateDimensions(contract Contract, observation Observation) error {
	expected := map[ComparisonDimension]bool{
		CompareResult:  false,
		CompareError:   false,
		CompareDBState: false,
		CompareMetrics: false,
	}
	for _, dimension := range contract.Comparison {
		expected[dimension] = true
	}
	actual := map[ComparisonDimension]bool{
		CompareResult:  observation.Result != nil,
		CompareError:   observation.Error != nil,
		CompareDBState: observation.DBState != nil,
		CompareMetrics: observation.Metrics != nil,
	}
	for _, dimension := range []ComparisonDimension{CompareResult, CompareError, CompareDBState, CompareMetrics} {
		if expected[dimension] != actual[dimension] {
			if expected[dimension] {
				return fmt.Errorf("%s: comparison dimension %q requires a payload", contract.ID, dimension)
			}
			return fmt.Errorf("%s: payload %q is not declared as a comparison dimension", contract.ID, dimension)
		}
	}
	return nil
}
