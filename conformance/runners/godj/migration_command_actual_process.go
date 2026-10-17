//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/progresshans/godj/conformance/runners/godj/migrationcommandworker"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"golang.org/x/sys/unix"
)

const migrationCommandActualTimeout = 90 * time.Second

type migrationCommandActualExecution struct {
	report      productcheck.MigrateReport
	stdout      []byte
	stderr      []byte
	environment []string
}

type migrationCommandActualParticipant struct {
	pid            int
	parentPID      int
	mode           string
	trace          []string
	privateRequest []byte
	privateReply   []byte
	privateStderr  []byte
}

func newMigrationCommandActualProject() (migrationCommandProject, error) {
	project, err := newMigrationCommandProject()
	if err != nil {
		return migrationCommandProject{}, err
	}
	fail := func(primary error) (migrationCommandProject, error) {
		return migrationCommandProject{}, errors.Join(primary, project.close())
	}
	repository, err := systemStateRepositoryRoot()
	if err != nil {
		return fail(err)
	}
	trace := filepath.Join(project.universe, "trace")
	command := filepath.Join(project.root, "cmd", "site")
	migrationsDirectory := filepath.Join(project.root, "migrations")
	for _, directory := range []string{trace, command, migrationsDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fail(errors.New("create migration-command actual fixture directory"))
		}
	}

	rootModule, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		return fail(errors.New("read migration-command repository module"))
	}
	const declaration = "module github.com/progresshans/godj\n"
	if !bytes.HasPrefix(rootModule, []byte(declaration)) || bytes.Count(rootModule, []byte(declaration)) != 1 {
		return fail(errors.New("migration-command repository module declaration is unexpected"))
	}
	module := strings.Replace(string(rootModule), declaration, "module example.com/godj-migration-command-fixture\n", 1)
	module += fmt.Sprintf(
		"\nrequire github.com/progresshans/godj v0.0.0\n\nreplace github.com/progresshans/godj => %s\n",
		strconv.Quote(filepath.ToSlash(repository)),
	)
	if err := writeMigrationCommandActualFile(filepath.Join(project.root, "go.mod"), []byte(module)); err != nil {
		return fail(err)
	}
	rootSum, err := os.ReadFile(filepath.Join(repository, "go.sum"))
	if err != nil {
		return fail(errors.New("read migration-command repository sum"))
	}
	if err := writeMigrationCommandActualFile(filepath.Join(project.root, "go.sum"), rootSum); err != nil {
		return fail(err)
	}
	mainSource := []byte(`package main

import (
	"os"

	"github.com/progresshans/godj/conformance/runners/godj/migrationcommandworker"
)

func main() {
	os.Exit(migrationcommandworker.Main())
}
`)
	if err := writeMigrationCommandActualFile(filepath.Join(command, "main.go"), mainSource); err != nil {
		return fail(err)
	}
	for index, source := range migrationCommandSources() {
		name := fmt.Sprintf("%04d_%s.godj.json", index+1, source.SourceID)
		if err := writeMigrationCommandActualFile(filepath.Join(migrationsDirectory, name), source.Document); err != nil {
			return fail(err)
		}
	}
	project.trace = trace
	return project, nil
}

func writeMigrationCommandActualFile(path string, document []byte) error {
	if err := os.WriteFile(path, document, 0o600); err != nil {
		return errors.New("write migration-command actual fixture")
	}
	return nil
}

func (project migrationCommandProject) runActual(
	ctx context.Context,
	mode string,
	database string,
	secret string,
	interrupt <-chan struct{},
) migrationCommandActualExecution {
	environment := project.actualEnvironment(mode, database, secret)
	phaseContext, cancel := context.WithTimeout(ctx, migrationCommandActualTimeout)
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	report := productcheck.RunMigrate(productcheck.MigrateInvocation{
		Context:     phaseContext,
		CWD:         project.root,
		Args:        []string{"migrate"},
		Environment: environment,
		Stdout:      &stdout,
		Stderr:      &stderr,
		Interrupt:   interrupt,
	})
	return migrationCommandActualExecution{
		report:      report,
		stdout:      append([]byte(nil), stdout.Bytes()...),
		stderr:      append([]byte(nil), stderr.Bytes()...),
		environment: append([]string(nil), environment...),
	}
}

func (project migrationCommandProject) actualEnvironment(mode, database, secret string) []string {
	values := migrationCommandActualEnvironment(os.Environ())
	values[migrationcommandworker.EnvironmentMode] = mode
	values[migrationcommandworker.EnvironmentTrace] = project.trace
	if database != "" {
		values[migrationcommandworker.EnvironmentDatabase] = database
	} else {
		delete(values, migrationcommandworker.EnvironmentDatabase)
	}
	if secret != "" {
		values[migrationcommandworker.EnvironmentSecret] = secret
	} else {
		delete(values, migrationcommandworker.EnvironmentSecret)
	}
	values["TMPDIR"] = project.workspace
	values["GOWORK"] = "off"
	values["GOTOOLCHAIN"] = "local"
	values["GOENV"] = "off"
	values["GOFLAGS"] = ""
	values["GOCACHEPROG"] = ""
	values["GOPROXY"] = "off"
	return migrationCommandSortedEnvironment(values)
}

func migrationCommandActualEnvironment(entries []string) map[string]string {
	values := make(map[string]string, len(entries)+8)
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	delete(values, migrationcommandworker.EnvironmentDatabase)
	delete(values, migrationcommandworker.EnvironmentMode)
	delete(values, migrationcommandworker.EnvironmentTrace)
	delete(values, migrationcommandworker.EnvironmentSecret)
	return values
}

func migrationCommandSortedEnvironment(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = key + "=" + values[key]
	}
	return result
}

func migrationCommandActualSuccess(execution migrationCommandActualExecution) error {
	report := execution.report
	controlBytes := len(migrationcommandworker.PrivateStderrControl) + 1
	wantStdout, marshalErr := json.Marshal(struct {
		SourceCount         int    `json:"source_count"`
		DefinitionCount     int    `json:"definition_count"`
		DefinitionSetDigest string `json:"definition_set_digest"`
	}{
		SourceCount:         report.MigrateResult.SourceCount,
		DefinitionCount:     report.MigrateResult.DefinitionCount,
		DefinitionSetDigest: report.MigrateResult.DefinitionSetDigest,
	})
	wantStdout = append(wantStdout, '\n')
	if report.ExitCode != 0 || !report.HasMigrateResult || report.HasMigrateFailure ||
		marshalErr != nil || !bytes.Equal(execution.stdout, wantStdout) || len(execution.stderr) != 0 ||
		report.BuildCalls != 1 || report.RunnerCalls != 1 ||
		report.DirectChildReaps != 2 || report.GroupSIGINTAttempts != 0 ||
		report.GroupSIGKILLAttempts != 0 || report.TempCreated != 1 ||
		report.TempCleanupAttempts != 1 || report.CleanupFailed != 0 || report.ResidualTemp != 0 ||
		report.RunnerResponseWrites != 1 || report.UserStdoutWrites != 1 || report.UserStderrWrites != 0 ||
		report.PartialStdoutWrites != 0 || report.RunnerStderrRetainedBytes != controlBytes ||
		report.RunnerStderrTruncated || !report.RawDiagnosticsDiscarded {
		return fmt.Errorf(
			"migration-command actual success = exit:%d result:%t failure:%t stdout:%d stderr:%d build:%d runner:%d reaps:%d sigint:%d sigkill:%d temp:%d/%d cleanup:%d residue:%d",
			report.ExitCode, report.HasMigrateResult, report.HasMigrateFailure,
			len(execution.stdout), len(execution.stderr), report.BuildCalls, report.RunnerCalls,
			report.DirectChildReaps, report.GroupSIGINTAttempts, report.GroupSIGKILLAttempts,
			report.TempCreated, report.TempCleanupAttempts, report.CleanupFailed, report.ResidualTemp,
		)
	}
	return nil
}

func migrationCommandActualFailure(
	execution migrationCommandActualExecution,
	category string,
	code string,
	wantSIGINT int,
	wantSIGKILL int,
) error {
	report := execution.report
	controlBytes := len(migrationcommandworker.PrivateStderrControl) + 1
	wantStderr := []byte(category + "/" + code + "\n")
	if report.ExitCode == 0 || report.HasMigrateResult || !report.HasMigrateFailure ||
		report.MigrateFailure.Category != category || report.MigrateFailure.Code != code ||
		len(execution.stdout) != 0 || !bytes.Equal(execution.stderr, wantStderr) ||
		report.BuildCalls != 1 || report.RunnerCalls != 1 || report.DirectChildReaps != 2 ||
		report.GroupSIGINTAttempts != wantSIGINT || report.GroupSIGKILLAttempts != wantSIGKILL ||
		report.TempCreated != 1 || report.TempCleanupAttempts != 1 ||
		report.CleanupFailed != 0 || report.ResidualTemp != 0 ||
		report.RunnerResponseWrites != 1 || report.UserStdoutWrites != 0 || report.UserStderrWrites != 1 ||
		report.PartialStdoutWrites != 0 || report.RunnerStderrRetainedBytes != controlBytes ||
		report.RunnerStderrTruncated || !report.RawDiagnosticsDiscarded {
		return fmt.Errorf(
			"migration-command actual failure = exit:%d result:%t failure:%+v stdout:%d stderr:%d build:%d runner:%d reaps:%d sigint:%d/%d sigkill:%d/%d temp:%d/%d cleanup:%d residue:%d",
			report.ExitCode, report.HasMigrateResult, report.MigrateFailure,
			len(execution.stdout), len(execution.stderr), report.BuildCalls, report.RunnerCalls,
			report.DirectChildReaps, report.GroupSIGINTAttempts, wantSIGINT,
			report.GroupSIGKILLAttempts, wantSIGKILL, report.TempCreated, report.TempCleanupAttempts,
			report.CleanupFailed, report.ResidualTemp,
		)
	}
	return nil
}

func migrationCommandActualParticipants(project migrationCommandProject) ([]migrationCommandActualParticipant, error) {
	entries, err := os.ReadDir(project.trace)
	if err != nil {
		return nil, errors.New("read migration-command actual trace")
	}
	common := map[string]map[int]struct{}{
		"start":            {},
		"trace":            {},
		"private-request":  {},
		"private-response": {},
		"private-stderr":   {},
	}
	secretAccess := make(map[int]struct{})
	ready := make(map[int]struct{})
	interruptReady := make(map[int]struct{})
	winnerMarker := false
	contenderMarker := false
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("migration-command actual trace entry %q is not a regular file", entry.Name())
		}
		switch entry.Name() {
		case "winner-lock":
			winnerMarker = true
			continue
		case "contender-observed":
			contenderMarker = true
			continue
		}
		matched := false
		for prefix, pids := range common {
			pid, ok := migrationCommandActualArtifactPID(entry.Name(), prefix)
			if !ok {
				continue
			}
			if _, duplicate := pids[pid]; duplicate {
				return nil, fmt.Errorf("migration-command actual trace duplicated %s PID %d", prefix, pid)
			}
			pids[pid] = struct{}{}
			matched = true
			break
		}
		if matched {
			continue
		}
		for prefix, pids := range map[string]map[int]struct{}{
			"secret-access":   secretAccess,
			"ready":           ready,
			"interrupt-ready": interruptReady,
		} {
			pid, ok := migrationCommandActualArtifactPID(entry.Name(), prefix)
			if !ok {
				continue
			}
			if _, duplicate := pids[pid]; duplicate {
				return nil, fmt.Errorf("migration-command actual trace duplicated %s PID %d", prefix, pid)
			}
			pids[pid] = struct{}{}
			matched = true
			break
		}
		if !matched {
			return nil, fmt.Errorf("migration-command actual trace contains unexpected artifact %q", entry.Name())
		}
	}
	for prefix, pids := range common {
		if prefix == "start" {
			continue
		}
		if !migrationCommandPIDSetEqual(common["start"], pids) {
			return nil, fmt.Errorf("migration-command actual %s PID set does not match start markers", prefix)
		}
	}

	participants := make([]migrationCommandActualParticipant, 0, len(common["start"]))
	for pid := range common["start"] {
		entryName := "start-" + strconv.Itoa(pid)
		start, err := os.ReadFile(filepath.Join(project.trace, entryName))
		if err != nil {
			return nil, errors.New("read migration-command actual start marker")
		}
		participant, err := migrationCommandParseActualStart(pid, start)
		if err != nil {
			return nil, err
		}
		participant.trace, err = migrationCommandReadTrace(project.trace, "trace", pid)
		if err != nil {
			return nil, err
		}
		participant.privateRequest, err = migrationCommandReadActualFile(project.trace, "private-request", pid)
		if err != nil {
			return nil, err
		}
		participant.privateReply, err = migrationCommandReadActualFile(project.trace, "private-response", pid)
		if err != nil {
			return nil, err
		}
		participant.privateStderr, err = migrationCommandReadActualFile(project.trace, "private-stderr", pid)
		if err != nil {
			return nil, err
		}
		participants = append(participants, participant)
	}
	sort.Slice(participants, func(left, right int) bool { return participants[left].pid < participants[right].pid })
	secretParticipants := make(map[int]struct{})
	concurrencyParticipants := make(map[int]struct{})
	interruptParticipants := make(map[int]struct{})
	for _, participant := range participants {
		if participant.mode == migrationcommandworker.ModeSecretMissing ||
			participant.mode == migrationcommandworker.ModeSecretInvalid ||
			participant.mode == migrationcommandworker.ModeSecretNil {
			secretParticipants[participant.pid] = struct{}{}
		}
		if participant.mode == migrationcommandworker.ModeConcurrency {
			concurrencyParticipants[participant.pid] = struct{}{}
		}
		if participant.mode == migrationcommandworker.ModeInterrupt {
			interruptParticipants[participant.pid] = struct{}{}
		}
	}
	if !migrationCommandPIDSetEqual(secretParticipants, secretAccess) {
		return nil, errors.New("migration-command actual secret-access PID set is incomplete or excessive")
	}
	if !migrationCommandPIDSetEqual(concurrencyParticipants, ready) {
		return nil, errors.New("migration-command actual concurrency-ready PID set is incomplete or excessive")
	}
	if !migrationCommandPIDSetEqual(interruptParticipants, interruptReady) {
		return nil, errors.New("migration-command actual interrupt-ready PID set is incomplete or excessive")
	}
	if len(concurrencyParticipants) == 0 {
		if winnerMarker || contenderMarker {
			return nil, errors.New("migration-command actual trace has unexpected concurrency coordination markers")
		}
	} else if len(concurrencyParticipants) != 2 || !winnerMarker || !contenderMarker {
		return nil, errors.New("migration-command actual concurrency coordination inventory is incomplete")
	}
	return participants, nil
}

func migrationCommandActualArtifactPID(name, prefix string) (int, bool) {
	marker := prefix + "-"
	if !strings.HasPrefix(name, marker) {
		return 0, false
	}
	raw := strings.TrimPrefix(name, marker)
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 || raw != strconv.Itoa(pid) {
		return 0, false
	}
	return pid, true
}

func migrationCommandPIDSetEqual(left, right map[int]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for pid := range left {
		if _, ok := right[pid]; !ok {
			return false
		}
	}
	return true
}

func migrationCommandParseActualStart(pid int, document []byte) (migrationCommandActualParticipant, error) {
	fields := strings.Fields(string(document))
	if len(fields) != 3 {
		return migrationCommandActualParticipant{}, errors.New("migration-command actual start marker shape is invalid")
	}
	readInteger := func(field, key string) (int, error) {
		name, raw, ok := strings.Cut(field, "=")
		if !ok || name != key {
			return 0, errors.New("migration-command actual start marker field is invalid")
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || raw != strconv.Itoa(value) {
			return 0, errors.New("migration-command actual start marker integer is invalid")
		}
		return value, nil
	}
	markerPID, err := readInteger(fields[0], "pid")
	if err != nil || markerPID != pid {
		return migrationCommandActualParticipant{}, errors.New("migration-command actual start marker PID is invalid")
	}
	parentPID, err := readInteger(fields[1], "ppid")
	if err != nil {
		return migrationCommandActualParticipant{}, err
	}
	name, mode, ok := strings.Cut(fields[2], "=")
	if !ok || name != "mode" || mode == "" {
		return migrationCommandActualParticipant{}, errors.New("migration-command actual start marker mode is invalid")
	}
	return migrationCommandActualParticipant{pid: pid, parentPID: parentPID, mode: mode}, nil
}

func migrationCommandReadTrace(directory, prefix string, pid int) ([]string, error) {
	document, err := migrationCommandReadActualFile(directory, prefix, pid)
	if err != nil {
		return nil, err
	}
	if len(document) == 0 || document[len(document)-1] != '\n' {
		return nil, errors.New("migration-command actual trace is incomplete")
	}
	lines := strings.Split(strings.TrimSuffix(string(document), "\n"), "\n")
	for _, line := range lines {
		if line == "" || strings.ContainsAny(line, "\r\n") {
			return nil, errors.New("migration-command actual trace event is invalid")
		}
	}
	return lines, nil
}

func migrationCommandReadActualFile(directory, prefix string, pid int) ([]byte, error) {
	document, err := os.ReadFile(filepath.Join(directory, prefix+"-"+strconv.Itoa(pid)))
	if err != nil {
		return nil, errors.New("read migration-command actual participant artifact")
	}
	if len(document) > 1<<20 {
		return nil, errors.New("migration-command actual participant artifact exceeds bound")
	}
	return document, nil
}

func migrationCommandActualParticipantsByMode(
	participants []migrationCommandActualParticipant,
	mode string,
) []migrationCommandActualParticipant {
	result := make([]migrationCommandActualParticipant, 0, len(participants))
	for _, participant := range participants {
		if participant.mode == mode {
			result = append(result, participant)
		}
	}
	return result
}

func migrationCommandAssertActualParticipant(participant migrationCommandActualParticipant) error {
	if participant.pid <= 0 || participant.parentPID != os.Getpid() || participant.pid == participant.parentPID {
		return fmt.Errorf(
			"migration-command actual process identity = pid:%d ppid:%d owner:%d",
			participant.pid, participant.parentPID, os.Getpid(),
		)
	}
	if !bytes.Equal(participant.privateRequest, migrateprotocol.RequestDocument()) {
		return errors.New("migration-command actual private request is not canonical")
	}
	wantPrivateStderr := []byte(migrationcommandworker.PrivateStderrControl + "\n")
	if !bytes.Equal(participant.privateStderr, wantPrivateStderr) {
		return fmt.Errorf(
			"migration-command actual private stderr positive control = %q, want exact fixed control",
			participant.privateStderr,
		)
	}
	if _, failure, failed := migrateprotocol.ParseResponse(participant.privateReply, true); failed || failure != (migrateprotocol.Failure{}) {
		return fmt.Errorf(
			"migration-command actual private response is invalid: failed=%t category=%q code=%q",
			failed, failure.Category, failure.Code,
		)
	}
	return nil
}

func migrationCommandTraceCount(trace []string, exact string) int {
	count := 0
	for _, event := range trace {
		if event == exact {
			count++
		}
	}
	return count
}

func migrationCommandTracePrefix(trace []string, prefix string) []string {
	result := make([]string, 0)
	for _, event := range trace {
		if strings.HasPrefix(event, prefix) {
			result = append(result, strings.TrimPrefix(event, prefix))
		}
	}
	return result
}

func migrationCommandActualDigestText(digest [sha256.Size]byte) string {
	return hex.EncodeToString(digest[:])
}

func migrationCommandWaitForActualMarker(ctx context.Context, directory, prefix string) (int, error) {
	timer := time.NewTimer(migrationCommandActualTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return 0, errors.New("read migration-command actual marker directory")
		}
		pids := make([]int, 0, 1)
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), prefix+"-") {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), prefix+"-"))
			if err != nil || pid <= 0 || entry.Name() != prefix+"-"+strconv.Itoa(pid) {
				return 0, errors.New("migration-command actual marker PID is invalid")
			}
			pids = append(pids, pid)
		}
		if len(pids) == 1 {
			return pids[0], nil
		}
		if len(pids) > 1 {
			return 0, errors.New("migration-command actual marker has excess participants")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timer.C:
			return 0, errors.New("migration-command actual marker timed out")
		case <-ticker.C:
		}
	}
}

func migrationCommandWaitForProcessGroupAbsent(ctx context.Context, processGroup int) error {
	if processGroup <= 0 {
		return errors.New("migration-command process group is invalid")
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := unix.Kill(-processGroup, 0)
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, unix.EPERM) {
			return errors.New("inspect migration-command process group")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("migration-command process group remained live")
		case <-ticker.C:
		}
	}
}

func migrationCommandActualArtifactOccurrences(directory, value string) (int, error) {
	needle := []byte(value)
	occurrences := 0
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return errors.New("migration-command actual artifact is invalid")
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		occurrences += bytes.Count(document, needle)
		return nil
	})
	return occurrences, err
}

func migrationCommandAssertActualDirectoryEmpty(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return errors.New("read migration-command actual cleanup directory")
	}
	if len(entries) != 0 {
		return fmt.Errorf("migration-command actual cleanup directory retained %d entries", len(entries))
	}
	return nil
}

func migrationCommandActualEnvironmentOccurrences(environment []string, value string) int {
	occurrences := 0
	for _, entry := range environment {
		occurrences += strings.Count(entry, value)
	}
	return occurrences
}

func migrationCommandActualArgvAndStdinOccurrences(value string) int {
	occurrences := strings.Count("migrate", value)
	occurrences += bytes.Count(migrateprotocol.RequestDocument(), []byte(value))
	return occurrences
}

func migrationCommandActualSecretCanary() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", errors.New("generate migration-command secret canary")
	}
	return "gdj-" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func migrationCommandAssertGlobalSourceOwnership(repository string) error {
	forbiddenLiterals := map[string]struct{}{
		"GODJ_MIGRATION_COMMAND_DATABASE": {},
		"GODJ_MIGRATION_COMMAND_MODE":     {},
		"GODJ_MIGRATION_COMMAND_TRACE":    {},
		"GODJ_MIGRATION_COMMAND_SECRET":   {},
		"GODJ_ARTICLE_SQLITE_DATABASE":    {},
		"GODJ_ARTICLE_POSTGRES_URL":       {},
		"GODJ_ARTICLE_POSTGRES_SCHEMA":    {},
	}
	forbiddenImports := map[string]struct{}{
		"github.com/progresshans/godj/db/sqlite":                       {},
		"github.com/progresshans/godj/db/postgres":                     {},
		"github.com/progresshans/godj/examples/article/databaseconfig": {},
	}
	for _, relative := range []string{"cmd/godj", "internal/projectcheck"} {
		root := filepath.Join(repository, filepath.FromSlash(relative))
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("migration-command global source %q is not a regular file", path)
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse migration-command global source %q: %w", path, err)
			}
			for _, spec := range parsed.Imports {
				value, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return fmt.Errorf("parse migration-command global import %q: %w", path, err)
				}
				if _, forbidden := forbiddenImports[value]; forbidden {
					return fmt.Errorf("migration-command global source %q owns forbidden import %q", path, value)
				}
			}
			var violation string
			ast.Inspect(parsed, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err == nil {
					if _, forbidden := forbiddenLiterals[value]; forbidden {
						violation = value
						return false
					}
				}
				return true
			})
			if violation != "" {
				return fmt.Errorf("migration-command global source %q owns forbidden environment key %q", path, violation)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

var _ io.Writer = (*bytes.Buffer)(nil)
