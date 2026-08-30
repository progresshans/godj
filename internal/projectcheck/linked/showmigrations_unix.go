//go:build darwin || linux

package linked

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
)

const (
	showMigrationsHistoryRecordLimit = 2_048
	showMigrationsIdentityByteLimit  = 1 << 20
	showMigrationsHistoryByteLimit   = 16 << 20
	showMigrationsCleanupTimeout     = time.Second
)

// ShowMigrationsConfig contains only invocation-local status inputs. The
// backend opener is called after the complete definition catalog has loaded.
type ShowMigrationsConfig struct {
	ProjectRoot                string
	MigrationDefinitionRoots   []string
	MigrationDefinitionSources []definition.Source
	OpenMigrationBackend       func(context.Context) (MigrationBackend, error)
}

// RunShowMigrations executes the sole private read-only migration-status
// command. Completed product failures are written as closed, detail-free
// responses. Caller-owned I/O and invariant failures remain Go errors.
func RunShowMigrations(
	ctx context.Context,
	config ShowMigrationsConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) (Report, error) {
	owned := ShowMigrationsConfig{
		ProjectRoot:                config.ProjectRoot,
		MigrationDefinitionRoots:   append([]string(nil), config.MigrationDefinitionRoots...),
		MigrationDefinitionSources: cloneDefinitionSources(config.MigrationDefinitionSources),
		OpenMigrationBackend:       config.OpenMigrationBackend,
	}
	return runShowMigrations(
		ctx,
		owned,
		append([]string(nil), argv...),
		stdin,
		stdout,
		systemDependencies{},
	)
}

func runShowMigrations(
	ctx context.Context,
	config ShowMigrationsConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
	dependencies systemDependencies,
) (Report, error) {
	var report Report
	if ctx == nil {
		return report, errors.New("project linked showmigrations: nil context")
	}
	if stdin == nil {
		return report, errors.New("project linked showmigrations: nil stdin")
	}
	if stdout == nil {
		return report, errors.New("project linked showmigrations: nil stdout")
	}
	if len(argv) != 1 || argv[0] != showmigrationsprotocol.PrivateArgument {
		return report, errors.New("project linked showmigrations: invalid private argv")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	requestFailure, requestFailed, err := showmigrationsprotocol.ReadRequest(stdin)
	if err != nil {
		return report, err
	}
	if requestFailed {
		return completeShowMigrationsResponse(
			ctx,
			dependencies,
			stdout,
			report,
			showmigrationsprotocol.Response{Failure: requestFailure},
			true,
		)
	}
	report.CommandDispatches = 1
	if err := ctx.Err(); err != nil {
		return report, err
	}

	loaded, _, catalogFailure, catalogFailed, err := loadCatalog(
		ctx,
		Config{
			ProjectRoot:                config.ProjectRoot,
			MigrationDefinitionRoots:   config.MigrationDefinitionRoots,
			MigrationDefinitionSources: config.MigrationDefinitionSources,
		},
		&report,
		dependencies,
	)
	if err != nil {
		return report, err
	}
	if catalogFailed {
		failure := showmigrationsprotocol.Failure{
			Category: catalogFailure.Category,
			Code:     catalogFailure.Code,
		}
		if !showmigrationsprotocol.IsLinkedFailure(failure) {
			return report, errors.New("project linked showmigrations: invalid catalog failure")
		}
		return completeShowMigrationsResponse(
			ctx,
			dependencies,
			stdout,
			report,
			showmigrationsprotocol.Response{Failure: failure},
			true,
		)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if config.OpenMigrationBackend == nil {
		return completeShowMigrationsResponse(
			ctx,
			dependencies,
			stdout,
			report,
			showmigrationsprotocol.Response{Failure: showmigrationsprotocol.Failure{
				Category: showmigrationsprotocol.CategoryBackend,
				Code:     showmigrationsprotocol.CodeInvalidBackend,
			}},
			true,
		)
	}

	report.BackendOpenCalls++
	opened, openErr := config.OpenMigrationBackend(ctx)
	backendAcquired := !isNilMigrationBackend(opened)
	if openErr != nil {
		cleanupFailed := false
		if backendAcquired {
			report.BackendCloseCalls++
			cleanupFailed = opened.Close() != nil
		}
		return completeShowMigrationsResponse(
			ctx,
			dependencies,
			stdout,
			report,
			showmigrationsprotocol.Response{Failure: showmigrationsprotocol.Failure{
				Category:      showmigrationsprotocol.CategoryBackend,
				Code:          showmigrationsprotocol.CodeBackendOpenFailed,
				CleanupFailed: cleanupFailed,
			}},
			false,
		)
	}
	if !backendAcquired {
		return completeShowMigrationsResponse(
			ctx,
			dependencies,
			stdout,
			report,
			showmigrationsprotocol.Response{Failure: showmigrationsprotocol.Failure{
				Category: showmigrationsprotocol.CategoryBackend,
				Code:     showmigrationsprotocol.CodeInvalidBackend,
			}},
			false,
		)
	}

	report.RevisionSessionOpens++
	session, sessionOpenErr := opened.OpenRevisionFencedSession(ctx)
	sessionAcquired := !isNilRevisionSession(session)
	if sessionOpenErr != nil || !sessionAcquired {
		failure := showmigrationsprotocol.Failure{
			Category: showmigrationsprotocol.CategoryBackend,
			Code:     showmigrationsprotocol.CodeInvalidBackend,
		}
		if sessionOpenErr != nil {
			failure = classifyShowMigrationsReadFailure(sessionOpenErr)
		}
		cleanupFailed := false
		if sessionAcquired {
			report.RevisionSessionCloses++
			cleanupFailed = closeShowMigrationsSession(ctx, session) != nil
		}
		report.BackendCloseCalls++
		if opened.Close() != nil {
			cleanupFailed = true
		}
		failure.CleanupFailed = cleanupFailed
		return completeShowMigrationsResponse(
			ctx,
			dependencies,
			stdout,
			report,
			showmigrationsprotocol.Response{Failure: failure},
			false,
		)
	}

	report.AppliedHistoryReads++
	applied, readErr := migrations.LoadAppliedState(ctx, boundedShowMigrationsReader{delegate: session})
	var rows []showmigrationsprotocol.Row
	statusErr := readErr
	if statusErr == nil {
		report.DirectPlannerCalls++
		statuses, statusesErr := loaded.Statuses(applied)
		statusErr = statusesErr
		if statusErr == nil {
			rows = make([]showmigrationsprotocol.Row, len(statuses))
			for index, status := range statuses {
				wireStatus, ok := showMigrationsWireStatus(status.Status)
				if !ok {
					statusErr = errors.New("project linked showmigrations: invalid core status")
					rows = nil
					break
				}
				rows[index] = showmigrationsprotocol.Row{
					App:    status.Key.App,
					Name:   status.Key.Name,
					Status: wireStatus,
				}
			}
			if statusErr == nil {
				_, encodeErr := showmigrationsprotocol.EncodeResponse(showmigrationsprotocol.Response{
					OK:     true,
					Result: showmigrationsprotocol.Result{Rows: rows},
				})
				if encodeErr != nil {
					statusErr = showMigrationsResourceError("migration status response exceeds the current wire boundary")
					rows = nil
				}
			}
		}
	}

	report.RevisionSessionCloses++
	sessionCloseErr := closeShowMigrationsSession(ctx, session)
	report.BackendCloseCalls++
	backendCloseErr := opened.Close()
	cleanupFailed := sessionCloseErr != nil || backendCloseErr != nil

	if statusErr == nil && !cleanupFailed {
		return completeShowMigrationsResponse(
			ctx,
			dependencies,
			stdout,
			report,
			showmigrationsprotocol.Response{
				OK:     true,
				Result: showmigrationsprotocol.Result{Rows: rows},
			},
			false,
		)
	}
	if statusErr == nil {
		return completeShowMigrationsResponse(
			ctx,
			dependencies,
			stdout,
			report,
			showmigrationsprotocol.Response{Failure: showmigrationsprotocol.Failure{
				Category:      showmigrationsprotocol.CategoryBackend,
				Code:          showmigrationsprotocol.CodeBackendCloseFailed,
				CleanupFailed: true,
			}},
			false,
		)
	}
	failure := classifyShowMigrationsReadFailure(statusErr)
	if cleanupFailed {
		failure.CleanupFailed = true
	}
	if !showmigrationsprotocol.IsLinkedFailure(failure) {
		return report, fmt.Errorf("project linked showmigrations: invalid classified failure")
	}
	return completeShowMigrationsResponse(
		ctx,
		dependencies,
		stdout,
		report,
		showmigrationsprotocol.Response{Failure: failure},
		false,
	)
}

type boundedShowMigrationsReader struct {
	delegate backend.AppliedMigrationReader
}

func (reader boundedShowMigrationsReader) ReadAppliedMigrations(ctx context.Context) ([]backend.AppliedMigration, error) {
	records, err := reader.delegate.ReadAppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}
	if len(records) > showMigrationsHistoryRecordLimit {
		return nil, showMigrationsResourceError("applied history record count exceeds current limit")
	}
	total := 0
	for _, record := range records {
		if !utf8.ValidString(record.App) || !utf8.ValidString(record.Name) {
			return nil, showMigrationsResourceError("applied history identity is not valid UTF-8")
		}
		if len(record.App) > showMigrationsIdentityByteLimit || len(record.Name) > showMigrationsIdentityByteLimit {
			return nil, showMigrationsResourceError("applied history identity exceeds current limit")
		}
		if len(record.App) > showMigrationsHistoryByteLimit-total {
			return nil, showMigrationsResourceError("applied history bytes exceed current limit")
		}
		total += len(record.App)
		if len(record.Name) > showMigrationsHistoryByteLimit-total {
			return nil, showMigrationsResourceError("applied history bytes exceed current limit")
		}
		total += len(record.Name)
	}
	return records, nil
}

func showMigrationsResourceError(message string) error {
	return &backend.RevisionFenceError{
		Kind:  backend.RevisionFenceFailureIntegrity,
		Cause: errors.New(message),
	}
}

func showMigrationsWireStatus(status migrations.MigrationStatus) (string, bool) {
	switch status {
	case migrations.MigrationStatusApplied:
		return showmigrationsprotocol.StatusApplied, true
	case migrations.MigrationStatusUnapplied:
		return showmigrationsprotocol.StatusUnapplied, true
	case migrations.MigrationStatusDefinitionMissing:
		return showmigrationsprotocol.StatusUnknown, true
	default:
		return "", false
	}
}

func classifyShowMigrationsReadFailure(err error) showmigrationsprotocol.Failure {
	var fence *backend.RevisionFenceError
	if errors.As(err, &fence) {
		switch fence.Kind {
		case backend.RevisionFenceFailureAdoptionRequired:
			return showmigrationsprotocol.Failure{
				Category: showmigrationsprotocol.CategoryCapability,
				Code:     string(migrations.CodeRevisionFenceAdoptionRequired),
			}
		case backend.RevisionFenceFailureStale:
			return showmigrationsprotocol.Failure{
				Category: showmigrationsprotocol.CategoryConflict,
				Code:     string(migrations.CodeStaleHistoryRevision),
			}
		case backend.RevisionFenceFailureContended:
			return showmigrationsprotocol.Failure{
				Category: showmigrationsprotocol.CategoryTransaction,
				Code:     string(migrations.CodeHistoryRevisionContended),
			}
		default:
			return showmigrationsprotocol.Failure{
				Category: showmigrationsprotocol.CategoryHistory,
				Code:     string(migrations.CodeHistoryRevisionIntegrity),
			}
		}
	}
	var planning *migrations.PlanningError
	if errors.As(err, &planning) && planning != nil {
		return showmigrationsprotocol.Failure{Category: string(planning.Category), Code: string(planning.Code)}
	}
	var recorder *migrations.RecorderError
	if errors.As(err, &recorder) && recorder != nil {
		return showmigrationsprotocol.Failure{Category: string(recorder.Category), Code: string(recorder.Code)}
	}
	// A raw session-open/read cause that is not owned by the revision-fence or
	// planning taxonomies is still a failed recorder snapshot, never a public
	// driver diagnostic.
	if err != nil {
		return showmigrationsprotocol.Failure{
			Category: showmigrationsprotocol.CategoryRecorder,
			Code:     string(migrations.CodeReadFailed),
		}
	}
	return showmigrationsprotocol.Failure{
		Category: showmigrationsprotocol.CategoryInternal,
		Code:     showmigrationsprotocol.CodeProjectInternalError,
	}
}

func completeShowMigrationsResponse(
	ctx context.Context,
	dependencies systemDependencies,
	writer io.Writer,
	report Report,
	response showmigrationsprotocol.Response,
	honorCancellation bool,
) (Report, error) {
	if dependencies.beforeResponseWrite != nil {
		dependencies.beforeResponseWrite()
	}
	if honorCancellation {
		if err := ctx.Err(); err != nil {
			return report, err
		}
	}
	report.RunnerResponseWrites++
	if err := showmigrationsprotocol.WriteResponse(writer, response); err != nil {
		return report, err
	}
	return report, nil
}

func closeShowMigrationsSession(ctx context.Context, session backend.RevisionFencedSession) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), showMigrationsCleanupTimeout)
	defer cancel()
	return session.Close(cleanupCtx)
}

func isNilRevisionSession(value backend.RevisionFencedSession) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
