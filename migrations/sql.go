package migrations

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations/backend"
)

const (
	// CategorySQLRender classifies renderer availability, execution, and
	// canonical-shape failures without retaining an arbitrary renderer cause.
	CategorySQLRender ErrorCategory = "migration_sql_render_error"
	// CategorySQLResource classifies bounded rendered-SQL resource failures.
	CategorySQLResource ErrorCategory = "migration_sql_resource_error"
)

const (
	CodeRendererUnavailable           ErrorCode = "renderer_unavailable"
	CodeRenderFailed                  ErrorCode = "render_failed"
	CodeInvalidRenderedSQL            ErrorCode = "invalid_rendered_sql"
	CodeRenderedSQLResourceLimit      ErrorCode = "rendered_sql_resource_limit"
	migrationSQLMaxStatements                   = 2_048
	migrationSQLMaxAggregateBodyBytes           = 16 << 20
)

// MigrationSQLError is the stable, secret-free failure returned by migration
// SQL projection. It deliberately has no Cause field and no Unwrap method:
// arbitrary renderer errors and partial SQL are never retained by the public
// migration core.
type MigrationSQLError struct {
	Category ErrorCategory
	Code     ErrorCode
}

func (e *MigrationSQLError) Error() string {
	if e == nil {
		return "migration SQL error"
	}
	return fmt.Sprintf("%s/%s", e.Category, e.Code)
}

// RenderMigrationSQL reconstructs the target's dependency-before historical
// state from one complete loader publication, materializes exactly that
// migration's forward intent, and invokes renderer exactly once. It performs
// no database, history, recorder, transaction, or schema-editor I/O.
func RenderMigrationSQL(
	ctx context.Context,
	loaded LoadedDefinitionSet,
	target MigrationKey,
	renderer backend.MigrationSQLRenderer,
) ([]string, error) {
	step := PlanStep{Key: target, Direction: DirectionForward}
	if ctx == nil {
		return nil, executionContextError(step, fmt.Errorf("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return nil, executionContextError(step, err)
	}

	snapshot, ok := loaded.snapshot()
	if !ok {
		return nil, invalidLoadedState(Migration{}, NoOperation, "", fmt.Errorf("loaded definition set is not initialized"))
	}
	definitions := snapshot.Values
	if err := validateLoadedDefinitionResources(definitions); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, executionContextError(step, err)
	}

	// Reconstructor construction validates the complete graph, chronology, and
	// full forward/reverse historical readiness before exact target lookup.
	definitionSnapshot := cloneMigrationDefinitions(definitions)
	reconstructor, err := newLoadedStateReconstructorContext(ctx, definitionSnapshot)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, executionContextError(step, err)
	}

	projection, err := reconstructor.targetProjection([]MigrationKey{target})
	if err != nil {
		return nil, err
	}
	if len(projection) == 0 || projection[len(projection)-1].Key != target ||
		projection[len(projection)-1].Direction != DirectionForward {
		return nil, invalidLoadedState(
			Migration{App: target.App, Name: target.Name},
			NoOperation,
			"",
			fmt.Errorf("target projection does not end with exactly one forward target"),
		)
	}
	for index := 0; index < len(projection)-1; index++ {
		if projection[index].Key == target {
			return nil, invalidLoadedState(
				Migration{App: target.App, Name: target.Name},
				NoOperation,
				"",
				fmt.Errorf("target projection repeats the exact target"),
			)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, executionContextError(step, err)
	}

	// Replay only the target's dependency closure. The target itself is then
	// materialized once against that exact before-state builder.
	builder := newLoadedStateBuilder()
	for _, dependency := range withoutExplicitTargets(projection, []MigrationKey{target}) {
		if err := ctx.Err(); err != nil {
			return nil, executionContextError(dependency, err)
		}
		migration, exists := reconstructor.definitions[dependency.Key]
		if !exists {
			return nil, invalidLoadedState(
				Migration{App: dependency.Key.App, Name: dependency.Key.Name},
				NoOperation,
				"",
				fmt.Errorf("target-before projection has no migration definition"),
			)
		}
		if err := reconstructor.applyLoadedMigrationContext(ctx, builder, migration, DirectionForward); err != nil {
			return nil, err
		}
	}
	materialized, err := reconstructor.materializeLoadedStep(ctx, builder, step, false)
	if err != nil {
		return nil, err
	}
	intent := loadedBackendRelationIntent(materialized.intent)
	wantStatements := len(intent.Operations)
	request := backend.ForwardMigrationSQLRequest{
		App:    strings.Clone(target.App),
		Name:   strings.Clone(target.Name),
		Intent: intent,
	}
	if err := ctx.Err(); err != nil {
		return nil, executionContextError(step, err)
	}

	// Renderer availability intentionally follows complete catalog validation,
	// exact lookup, target-before reconstruction, and request materialization.
	if isNilInterface(renderer) {
		return nil, newMigrationSQLError(CategorySQLRender, CodeRendererUnavailable)
	}
	statements, renderErr := renderer.RenderForwardMigrationSQL(ctx, request)
	if err := ctx.Err(); err != nil {
		return nil, executionContextError(step, err)
	}
	if renderErr != nil {
		if backend.IsCapabilityError(renderErr) {
			return nil, newMigrationSQLError(CategoryCapability, CodeUnsupported)
		}
		return nil, newMigrationSQLError(CategorySQLRender, CodeRenderFailed)
	}

	return validateRenderedMigrationSQL(ctx, step, statements, wantStatements)
}

func validateRenderedMigrationSQL(
	ctx context.Context,
	step PlanStep,
	statements []string,
	wantStatements int,
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, executionContextError(step, err)
	}
	// Resource bounds always precede semantic inspection. Keep the arithmetic
	// subtraction-based so it remains safe on every supported integer width.
	if len(statements) > migrationSQLMaxStatements {
		return nil, newMigrationSQLError(CategorySQLResource, CodeRenderedSQLResourceLimit)
	}
	total := 0
	for index := range statements {
		if err := ctx.Err(); err != nil {
			return nil, executionContextError(step, err)
		}
		bodyBytes := len(statements[index])
		if bodyBytes > migrationSQLMaxAggregateBodyBytes-total {
			return nil, newMigrationSQLError(CategorySQLResource, CodeRenderedSQLResourceLimit)
		}
		total += bodyBytes
	}

	if len(statements) != wantStatements {
		return nil, newMigrationSQLError(CategorySQLRender, CodeInvalidRenderedSQL)
	}
	result := make([]string, len(statements))
	for index := range statements {
		if err := ctx.Err(); err != nil {
			return nil, executionContextError(step, err)
		}
		body := statements[index]
		if !validMigrationSQLBody(body) {
			return nil, newMigrationSQLError(CategorySQLRender, CodeInvalidRenderedSQL)
		}
		result[index] = strings.Clone(body)
	}
	return result, nil
}

func validMigrationSQLBody(body string) bool {
	if body == "" || !utf8.ValidString(body) || strings.ContainsRune(body, ';') {
		return false
	}
	if migrationSQLASCIIWhitespace(body[0]) || migrationSQLASCIIWhitespace(body[len(body)-1]) {
		return false
	}
	for _, character := range body {
		if character != '\n' && unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func migrationSQLASCIIWhitespace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

func newMigrationSQLError(category ErrorCategory, code ErrorCode) *MigrationSQLError {
	return &MigrationSQLError{
		Category: category,
		Code:     code,
	}
}
