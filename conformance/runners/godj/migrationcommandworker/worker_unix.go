//go:build darwin || linux

// Package migrationcommandworker is the project-linked executable body used
// by the GDJ-0049 conformance runner. It deliberately records only bounded,
// secret-free lifecycle facts outside the private project-runner protocol.
package migrationcommandworker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/progresshans/godj/db/sqlite"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	godjproject "github.com/progresshans/godj/project"
	"github.com/progresshans/godj/schema/ir"
	"golang.org/x/sys/unix"
)

const (
	EnvironmentDatabase = "GODJ_MIGRATION_COMMAND_DATABASE"
	EnvironmentMode     = "GODJ_MIGRATION_COMMAND_MODE"
	EnvironmentTrace    = "GODJ_MIGRATION_COMMAND_TRACE"
	EnvironmentSecret   = "GODJ_MIGRATION_COMMAND_SECRET"

	ModeNormal        = "normal"
	ModeFailMiddle    = "fail_middle"
	ModeConcurrency   = "concurrency"
	ModeInterrupt     = "interrupt"
	ModeSecretMissing = "secret_missing"
	ModeSecretInvalid = "secret_invalid"
	ModeSecretNil     = "secret_typed_nil"

	PrivateStderrControl = "godj-migration-command-private-stderr-control"
	SecretDigestDomain   = "godj/conformance/migration-command-secret/v1:"
)

const (
	prefixMigration = "0001_prefix"
	middleMigration = "0002_middle"
	middleTable     = "godj_command_middle"
)

// Main runs one actual project child. Its return value is suitable for
// os.Exit. Product failures still exit zero because project.Run publishes a
// closed private response; only runner/protocol failures are non-zero.
func Main() int {
	traceDirectory, mode, err := configuration()
	if err != nil {
		_, _ = io.WriteString(os.Stderr, "migration command project runner failed\n")
		return 1
	}
	pid := os.Getpid()
	capture, err := openPrivateCapture(traceDirectory, pid)
	if err != nil {
		_, _ = io.WriteString(os.Stderr, "migration command project runner failed\n")
		return 1
	}
	stderrRedirect, err := startStderrRedirect(capture.stderr)
	if err != nil {
		_ = capture.close()
		_, _ = io.WriteString(os.Stderr, "migration command project runner failed\n")
		return 1
	}
	control := []byte(PrivateStderrControl + "\n")
	written, controlErr := unix.Write(int(os.Stderr.Fd()), control)
	if controlErr != nil || written != len(control) {
		_ = stderrRedirect.stop()
		_ = capture.close()
		_, _ = io.WriteString(os.Stderr, "migration command project runner failed\n")
		return 1
	}
	if err := writeStartMarker(traceDirectory, pid, os.Getppid(), mode); err != nil {
		_ = stderrRedirect.stop()
		_ = capture.close()
		_, _ = io.WriteString(os.Stderr, "migration command project runner failed\n")
		return 1
	}

	request := io.TeeReader(os.Stdin, capture.request)
	response := io.MultiWriter(capture.response, os.Stdout)
	runErr := godjproject.Run(
		context.Background(),
		godjproject.Config{
			MigrationDefinitionRoots: []string{"migrations"},
			OpenMigrationBackend: func(ctx context.Context) (godjproject.MigrationBackend, error) {
				return openBackend(ctx, traceDirectory, mode, pid)
			},
		},
		os.Args[1:],
		request,
		response,
	)
	if runErr != nil {
		_, _ = io.WriteString(os.Stderr, "migration command project runner failed\n")
	}
	cleanupErr := errors.Join(stderrRedirect.stop(), capture.close())
	if runErr != nil || cleanupErr != nil {
		return 1
	}
	return 0
}

func configuration() (string, string, error) {
	directory := strings.TrimSpace(os.Getenv(EnvironmentTrace))
	mode := strings.TrimSpace(os.Getenv(EnvironmentMode))
	if directory == "" || !filepath.IsAbs(directory) {
		return "", "", errors.New("migration command trace directory is invalid")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("migration command trace directory is invalid")
	}
	switch mode {
	case ModeNormal, ModeFailMiddle, ModeConcurrency, ModeInterrupt,
		ModeSecretMissing, ModeSecretInvalid, ModeSecretNil:
		return directory, mode, nil
	default:
		return "", "", errors.New("migration command mode is invalid")
	}
}

func openBackend(ctx context.Context, directory, mode string, pid int) (godjproject.MigrationBackend, error) {
	if err := appendEvent(directory, pid, "backend_open_attempt"); err != nil {
		return nil, err
	}
	if isSecretMode(mode) {
		secret, present := os.LookupEnv(EnvironmentSecret)
		if !present || secret == "" {
			return nil, errors.New("migration command secret configuration is absent")
		}
		secretDigest := sha256.Sum256([]byte(SecretDigestDomain + secret))
		if err := writeExclusive(
			filepath.Join(directory, "secret-access-"+strconv.Itoa(pid)),
			[]byte(fmt.Sprintf("pid=%d\nppid=%d\nmode=%s\ndigest=%x\n", pid, os.Getppid(), mode, secretDigest)),
		); err != nil {
			return nil, errors.New("record migration command secret access")
		}
		switch mode {
		case ModeSecretMissing:
			return nil, errors.New("missing backend configuration: " + secret)
		case ModeSecretInvalid:
			return nil, errors.New("invalid backend configuration: " + secret)
		case ModeSecretNil:
			var typedNil *typedNilBackend
			return typedNil, nil
		}
	}

	database := strings.TrimSpace(os.Getenv(EnvironmentDatabase))
	if database == "" {
		return nil, errors.New("migration command database configuration is absent")
	}
	opened, err := sqlite.Open(ctx, database)
	if err != nil {
		return nil, err
	}
	backend := &observedBackend{
		MigrationBackend: opened,
		directory:        directory,
		mode:             mode,
		pid:              pid,
	}
	if err := backend.event("backend_open_complete"); err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	return backend, nil
}

func isSecretMode(mode string) bool {
	switch mode {
	case ModeSecretMissing, ModeSecretInvalid, ModeSecretNil:
		return true
	default:
		return false
	}
}

type typedNilBackend struct {
	godjproject.MigrationBackend
}

type observedBackend struct {
	godjproject.MigrationBackend
	directory string
	mode      string
	pid       int
}

func (value *observedBackend) event(event string) error {
	return appendEvent(value.directory, value.pid, event)
}

func (value *observedBackend) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	if err := value.event("session_attempt"); err != nil {
		return nil, err
	}
	session, err := value.MigrationBackend.OpenRevisionFencedSession(ctx)
	if err != nil || session == nil {
		return session, err
	}
	if err := value.event("session_open_complete"); err != nil {
		return nil, errors.Join(err, session.Close(ctx))
	}
	return &observedSession{
		RevisionFencedSession: session,
		directory:             value.directory,
		mode:                  value.mode,
		pid:                   value.pid,
	}, nil
}

func (value *observedBackend) Close() error {
	if err := value.event("backend_close_attempt"); err != nil {
		return errors.Join(err, value.MigrationBackend.Close())
	}
	err := value.MigrationBackend.Close()
	if err == nil {
		err = value.event("backend_close_complete")
	}
	return err
}

type observedSession struct {
	migrationbackend.RevisionFencedSession
	directory string
	mode      string
	pid       int
	winner    bool
	barrier   bool
}

func (value *observedSession) event(event string) error {
	return appendEvent(value.directory, value.pid, event)
}

func (value *observedSession) ReadAppliedMigrations(ctx context.Context) ([]migrationbackend.AppliedMigration, error) {
	if err := value.event("snapshot_attempt"); err != nil {
		return nil, err
	}
	records, err := value.RevisionFencedSession.ReadAppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}
	if err := value.event("snapshot_complete:" + strconv.Itoa(len(records))); err != nil {
		return nil, err
	}
	if value.mode != ModeConcurrency {
		return records, nil
	}
	if len(records) != 0 {
		return nil, errors.New("migration command concurrency requires an empty snapshot")
	}
	marker := filepath.Join(value.directory, "ready-"+strconv.Itoa(value.pid))
	payload := []byte(fmt.Sprintf("pid=%d\nppid=%d\nrecords=%d\n", value.pid, os.Getppid(), len(records)))
	if err := writeExclusive(marker, payload); err != nil {
		return nil, errors.New("write migration command concurrency marker")
	}
	participants, err := waitForParticipants(ctx, value.directory, 2)
	if err != nil {
		return nil, err
	}
	value.barrier = true
	value.winner = value.pid == participants[0]
	return records, nil
}

func (value *observedSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	key := transition.Migration.App + "/" + transition.Migration.Name
	if err := value.event("begin_attempt:" + key); err != nil {
		return nil, err
	}
	if value.mode == ModeConcurrency && value.barrier {
		value.barrier = false
		if value.winner {
			transaction, err := value.RevisionFencedSession.BeginMigration(ctx, transition, intent)
			if err != nil || transaction == nil {
				return transaction, err
			}
			if err := value.event("begin_complete:" + key); err != nil {
				return transaction, err
			}
			if err := writeExclusive(
				filepath.Join(value.directory, "winner-lock"),
				[]byte(fmt.Sprintf("pid=%d\n", value.pid)),
			); err != nil {
				return transaction, errors.New("write migration command winner marker")
			}
			if err := waitForMarker(ctx, filepath.Join(value.directory, "contender-observed")); err != nil {
				return transaction, err
			}
			return &observedTransaction{
				RevisionFencedTransaction: transaction,
				directory:                 value.directory,
				mode:                      value.mode,
				pid:                       value.pid,
				key:                       key,
			}, nil
		}
		if err := waitForMarker(ctx, filepath.Join(value.directory, "winner-lock")); err != nil {
			return nil, err
		}
		transaction, err := value.RevisionFencedSession.BeginMigration(ctx, transition, intent)
		status := "unexpected"
		var fence *migrationbackend.RevisionFenceError
		if errors.As(err, &fence) && fence != nil && fence.Kind == migrationbackend.RevisionFenceFailureContended {
			status = "contended"
		}
		payload := []byte(fmt.Sprintf("pid=%d\nstatus=%s\n", value.pid, status))
		if writeErr := writeExclusive(filepath.Join(value.directory, "contender-observed"), payload); writeErr != nil {
			return transaction, errors.New("write migration command contender marker")
		}
		if status != "contended" {
			return transaction, errors.New("migration command contender was not revision fenced")
		}
		if eventErr := value.event("begin_contended:" + key); eventErr != nil {
			return transaction, eventErr
		}
		return transaction, err
	}

	transaction, err := value.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	if err != nil || transaction == nil {
		return transaction, err
	}
	if err := value.event("begin_complete:" + key); err != nil {
		return transaction, err
	}
	return &observedTransaction{
		RevisionFencedTransaction: transaction,
		directory:                 value.directory,
		mode:                      value.mode,
		pid:                       value.pid,
		key:                       key,
	}, nil
}

func (value *observedSession) Close(ctx context.Context) error {
	if err := value.event("session_close_attempt"); err != nil {
		return errors.Join(err, value.RevisionFencedSession.Close(ctx))
	}
	err := value.RevisionFencedSession.Close(ctx)
	if err == nil {
		err = value.event("session_close_complete")
	}
	return err
}

type observedTransaction struct {
	migrationbackend.RevisionFencedTransaction
	directory string
	mode      string
	pid       int
	key       string
}

func (value *observedTransaction) event(event string) error {
	return appendEvent(value.directory, value.pid, event)
}

func (value *observedTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	if err := value.event("create_attempt:" + model.DBTable); err != nil {
		return err
	}
	if err := value.RevisionFencedTransaction.CreateModel(ctx, model); err != nil {
		return err
	}
	if err := value.event("create_complete:" + model.DBTable); err != nil {
		return err
	}
	if value.mode == ModeFailMiddle && value.key == "command/"+middleMigration && model.DBTable == middleTable {
		if err := value.event("failure_injected:" + value.key); err != nil {
			return err
		}
		return errors.New("migration command injected middle failure")
	}
	if value.mode == ModeInterrupt && value.key == "command/"+middleMigration && model.DBTable == middleTable {
		if err := writeExclusive(
			filepath.Join(value.directory, "interrupt-ready-"+strconv.Itoa(value.pid)),
			[]byte(fmt.Sprintf("pid=%d\nppid=%d\n", value.pid, os.Getppid())),
		); err != nil {
			return errors.New("write migration command interrupt marker")
		}
		<-ctx.Done()
		if err := value.event("signal_context_canceled"); err != nil {
			return err
		}
		return ctx.Err()
	}
	return nil
}

func (value *observedTransaction) RecordApplied(ctx context.Context, app, name string) error {
	key := app + "/" + name
	if err := value.event("record_attempt:" + key); err != nil {
		return err
	}
	err := value.RevisionFencedTransaction.RecordApplied(ctx, app, name)
	if err == nil {
		err = value.event("record_complete:" + key)
	}
	return err
}

func (value *observedTransaction) CommitFenced(ctx context.Context) (migrationbackend.CommitOutcome, error) {
	if err := value.event("commit_attempt:" + value.key); err != nil {
		return migrationbackend.CommitOutcome{}, err
	}
	outcome, err := value.RevisionFencedTransaction.CommitFenced(ctx)
	if err == nil {
		err = value.event("commit_complete:" + value.key + ":" + strconv.Itoa(int(outcome.Durability)))
	}
	return outcome, err
}

func (value *observedTransaction) Rollback(ctx context.Context) error {
	if err := value.event("rollback_attempt:" + value.key); err != nil {
		return errors.Join(err, value.RevisionFencedTransaction.Rollback(ctx))
	}
	err := value.RevisionFencedTransaction.Rollback(ctx)
	if err == nil {
		err = value.event("rollback_complete:" + value.key)
	}
	return err
}

func appendEvent(directory string, pid int, event string) error {
	if strings.ContainsAny(event, "\r\n") {
		return errors.New("migration command trace event is invalid")
	}
	path := filepath.Join(directory, "trace-"+strconv.Itoa(pid))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return errors.New("open migration command trace")
	}
	_, writeErr := file.WriteString(event + "\n")
	return errors.Join(writeErr, file.Close())
}

func writeStartMarker(directory string, pid, parentPID int, mode string) error {
	payload := []byte(fmt.Sprintf("pid=%d\nppid=%d\nmode=%s\n", pid, parentPID, mode))
	return writeExclusive(filepath.Join(directory, "start-"+strconv.Itoa(pid)), payload)
}

func writeExclusive(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(payload)
	if written != len(payload) && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	return errors.Join(writeErr, file.Close())
}

func waitForParticipants(ctx context.Context, directory string, want int) ([]int, error) {
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, errors.New("read migration command concurrency directory")
		}
		participants := make([]int, 0, want)
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), "ready-") {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), "ready-"))
			if err != nil || pid <= 0 || entry.Name() != "ready-"+strconv.Itoa(pid) {
				return nil, errors.New("migration command concurrency participant is invalid")
			}
			participants = append(participants, pid)
		}
		sort.Ints(participants)
		if len(participants) == want {
			return participants, nil
		}
		if len(participants) > want {
			return nil, errors.New("migration command concurrency has excess participants")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, errors.New("migration command concurrency timed out")
		case <-ticker.C:
		}
	}
}

func waitForMarker(ctx context.Context, path string) error {
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("migration command coordination marker is invalid")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("inspect migration command coordination marker")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("migration command coordination timed out")
		case <-ticker.C:
		}
	}
}

type privateCapture struct {
	request  *os.File
	response *os.File
	stderr   *os.File
}

func openPrivateCapture(directory string, pid int) (*privateCapture, error) {
	capture := new(privateCapture)
	open := func(kind string) (*os.File, error) {
		return os.OpenFile(
			filepath.Join(directory, "private-"+kind+"-"+strconv.Itoa(pid)),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			0o600,
		)
	}
	var err error
	if capture.request, err = open("request"); err != nil {
		return nil, err
	}
	if capture.response, err = open("response"); err != nil {
		return nil, errors.Join(err, capture.request.Close())
	}
	if capture.stderr, err = open("stderr"); err != nil {
		return nil, errors.Join(err, capture.request.Close(), capture.response.Close())
	}
	return capture, nil
}

func (value *privateCapture) close() error {
	if value == nil {
		return nil
	}
	return errors.Join(value.request.Close(), value.response.Close(), value.stderr.Close())
}

type stderrRedirect struct {
	target   int
	original *os.File
	done     <-chan error
}

type stderrMirror struct {
	public  io.Writer
	capture io.Writer
	err     error
}

func (value *stderrMirror) Write(payload []byte) (int, error) {
	publicWritten, publicErr := value.public.Write(payload)
	if publicWritten != len(payload) && publicErr == nil {
		publicErr = io.ErrShortWrite
	}
	captureWritten, captureErr := value.capture.Write(payload)
	if captureWritten != len(payload) && captureErr == nil {
		captureErr = io.ErrShortWrite
	}
	value.err = errors.Join(value.err, publicErr, captureErr)
	// Always drain fd 2 fully. Destination failures are returned only after
	// EOF so a diagnostic writer cannot deadlock the owned child.
	return len(payload), nil
}

func startStderrRedirect(capture *os.File) (*stderrRedirect, error) {
	target := int(os.Stderr.Fd())
	originalFD, err := unix.Dup(target)
	if err != nil {
		return nil, errors.New("duplicate migration command stderr")
	}
	original := os.NewFile(uintptr(originalFD), "migration-command-stderr")
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = original.Close()
		return nil, errors.New("open migration command stderr pipe")
	}
	if err := unix.Dup2(int(writer.Fd()), target); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		_ = original.Close()
		return nil, errors.New("redirect migration command stderr")
	}
	if err := writer.Close(); err != nil {
		_ = unix.Dup2(int(original.Fd()), target)
		_ = reader.Close()
		_ = original.Close()
		return nil, errors.New("close migration command stderr writer")
	}
	done := make(chan error, 1)
	go func() {
		mirror := &stderrMirror{public: original, capture: capture}
		_, copyErr := io.Copy(mirror, reader)
		done <- errors.Join(copyErr, mirror.err, reader.Close())
	}()
	return &stderrRedirect{target: target, original: original, done: done}, nil
}

func (value *stderrRedirect) stop() error {
	if value == nil {
		return nil
	}
	restoreErr := unix.Dup2(int(value.original.Fd()), value.target)
	drainErr := <-value.done
	return errors.Join(restoreErr, drainErr, value.original.Close())
}
