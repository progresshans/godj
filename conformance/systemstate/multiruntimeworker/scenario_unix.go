//go:build darwin || linux

package multiruntimeworker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	maximumStderrBytes = 4 << 10
)

// RunScenario explicitly migrates and bootstraps an otherwise isolated target,
// runs two simultaneous writer processes through a real anonymous-pipe
// barrier, closes both, and opens a third process to inspect durable state.
// executable must be the absolute path to the package's cmd worker binary.
func RunScenario(ctx context.Context, executable string, database DatabaseConfig) (Facts, error) {
	if ctx == nil || ctx.Err() != nil {
		return Facts{}, newError(CodeContext)
	}
	if executable == "" || len(executable) > 4096 || strings.ContainsRune(executable, 0) || !filepath.IsAbs(executable) ||
		!database.valid() {
		return Facts{}, newError(CodeInvalidConfig)
	}
	password, objectID, err := scenarioIdentity()
	if err != nil {
		return Facts{}, err
	}
	preparation, err := newWireConfig(database, roleProbe, password, objectID)
	if err != nil {
		return Facts{}, err
	}
	if err := prepareScenarioDatabase(ctx, preparation); err != nil {
		return Facts{}, err
	}

	holderConfig, _ := newWireConfig(database, roleHolder, password, objectID)
	contenderConfig, _ := newWireConfig(database, roleContender, password, objectID)
	holder, err := startChild(ctx, executable, holderConfig)
	if err != nil {
		return Facts{}, err
	}
	holderDone := false
	defer func() {
		if !holderDone {
			holder.abort()
		}
	}()
	if err := holder.expectEvent(eventReady); err != nil {
		return Facts{}, err
	}

	contender, err := startChild(ctx, executable, contenderConfig)
	if err != nil {
		return Facts{}, err
	}
	contenderDone := false
	defer func() {
		if !contenderDone {
			contender.abort()
		}
	}()
	if err := contender.expectEvent(eventReady); err != nil {
		return Facts{}, err
	}

	if err := holder.sendControl(controlStart); err != nil {
		return Facts{}, err
	}
	if err := holder.expectEvent(eventAcquired); err != nil {
		return Facts{}, err
	}
	if err := contender.sendControl(controlStart); err != nil {
		return Facts{}, err
	}
	if err := contender.expectEvent(eventAttempted); err != nil {
		return Facts{}, err
	}

	// The contender itself emits a positive waiting proof only after its
	// CoordinatedAtomic call has remained outstanding for the bounded proof
	// window. An early callback emits eventAcquired instead, which fails this
	// exact event expectation rather than being synthesized as a pass from
	// parent-side silence.
	if err := contender.expectEvent(eventWaiting); err != nil {
		return Facts{}, err
	}
	if err := holder.sendControl(controlRelease); err != nil {
		return Facts{}, err
	}
	if err := contender.expectEvent(eventAcquired); err != nil {
		return Facts{}, err
	}

	holderResponse, err := holder.finish(roleHolder, database.state.kind)
	if err != nil {
		return Facts{}, err
	}
	holderDone = true
	contenderResponse, err := contender.finish(roleContender, database.state.kind)
	if err != nil {
		return Facts{}, err
	}
	contenderDone = true

	probeConfig, _ := newWireConfig(database, roleProbe, password, objectID)
	probe, err := startChild(ctx, executable, probeConfig)
	if err != nil {
		return Facts{}, err
	}
	probeDone := false
	defer func() {
		if !probeDone {
			probe.abort()
		}
	}()
	probeResponse, err := probe.finish(roleProbe, database.state.kind)
	if err != nil {
		return Facts{}, err
	}
	probeDone = true

	writerProcesses := 1
	if holderResponse.PID > 0 && contenderResponse.PID > 0 && holderResponse.PID != contenderResponse.PID {
		writerProcesses = 2
	}
	roleDivergence := probeResponse.HolderEvents != 1 || probeResponse.ContenderEvents != 1
	secrets := []string{
		password,
		database.state.sqliteDataSource,
		database.state.postgresURL,
		database.state.postgresSchema,
	}
	artifactSecretOccurrences, err := countSQLiteArtifactSecretOccurrences(database, secrets)
	if err != nil {
		return Facts{}, err
	}
	transportSecretOccurrences := countSecretOccurrences(
		[][]byte{
			holder.stdout.bytes(), holder.stderr.bytes(),
			contender.stdout.bytes(), contender.stderr.bytes(),
			probe.stdout.bytes(), probe.stderr.bytes(),
		},
		secrets,
	)
	facts := Facts{
		WriterProcesses: writerProcesses,
		SameSchema: holderResponse.Opened && contenderResponse.Opened && probeResponse.Opened &&
			holderResponse.Backend == database.state.kind && contenderResponse.Backend == database.state.kind &&
			probeResponse.Backend == database.state.kind,
		BarrierLinearized: holderResponse.CallbackInvocations == 1 &&
			contenderResponse.CallbackInvocations == 1 && holderResponse.EventAppended && contenderResponse.EventAppended,
		HolderCallbackInvocations:    holderResponse.CallbackInvocations,
		ContenderCallbackInvocations: contenderResponse.CallbackInvocations,
		RestartPreserved: probeResponse.HistoryCount == 2 && probeResponse.HolderEvents == 1 &&
			probeResponse.ContenderEvents == 1 && probeResponse.UnexpectedEvents == 0 && probeResponse.StrictlyIncreasing,
		DurableEvents: probeResponse.HistoryCount,
		Divergence:    roleDivergence,
		Loss:          probeResponse.HistoryCount < 2 || probeResponse.HolderEvents == 0 || probeResponse.ContenderEvents == 0,
		Drift: probeResponse.HistoryCount > 2 || probeResponse.UnexpectedEvents > 0 ||
			probeResponse.HolderEvents > 1 || probeResponse.ContenderEvents > 1 || !probeResponse.StrictlyIncreasing,
		SecretOccurrences: transportSecretOccurrences + artifactSecretOccurrences + probeResponse.DurableSecretOccurrences,
	}
	return facts, nil
}

func scenarioIdentity() (string, int64, error) {
	var material [40]byte
	if _, err := io.ReadFull(rand.Reader, material[:]); err != nil {
		return "", 0, newError(CodeProtocol)
	}
	password := "phase-e-" + base64.RawURLEncoding.EncodeToString(material[:32])
	objectID := int64(binary.BigEndian.Uint64(material[32:]) & uint64(^uint64(0)>>1))
	if objectID == 0 {
		objectID = 1
	}
	return password, objectID, nil
}

type boundedCapture struct {
	limit    int
	content  bytes.Buffer
	exceeded bool
}

func (capture *boundedCapture) Write(data []byte) (int, error) {
	if capture == nil || capture.limit < 1 || capture.exceeded || capture.content.Len()+len(data) > capture.limit {
		if capture != nil {
			capture.exceeded = true
		}
		return 0, newError(CodeProtocol)
	}
	return capture.content.Write(data)
}

func (capture *boundedCapture) bytes() []byte {
	if capture == nil {
		return nil
	}
	return capture.content.Bytes()
}

type childProcess struct {
	command       *exec.Cmd
	eventReader   *os.File
	controlWriter *os.File
	stdout        boundedCapture
	stderr        boundedCapture
	waited        bool
}

func startChild(ctx context.Context, executable string, config wireConfig) (*childProcess, error) {
	configReader, configWriter, err := os.Pipe()
	if err != nil {
		return nil, newError(CodeProcess)
	}
	eventReader, eventWriter, err := os.Pipe()
	if err != nil {
		_ = configReader.Close()
		_ = configWriter.Close()
		return nil, newError(CodeProcess)
	}
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		_ = configReader.Close()
		_ = configWriter.Close()
		_ = eventReader.Close()
		_ = eventWriter.Close()
		return nil, newError(CodeProcess)
	}

	child := &childProcess{
		eventReader:   eventReader,
		controlWriter: controlWriter,
		stdout:        boundedCapture{limit: maximumOutputBytes},
		stderr:        boundedCapture{limit: maximumStderrBytes},
	}
	command := exec.CommandContext(ctx, executable)
	command.Env = []string{"TZ=UTC"}
	command.Stdin = nil
	command.Stdout = &child.stdout
	command.Stderr = &child.stderr
	command.ExtraFiles = []*os.File{configReader, eventWriter, controlReader}
	command.WaitDelay = 5 * time.Second
	child.command = command
	if err := command.Start(); err != nil {
		_ = configReader.Close()
		_ = configWriter.Close()
		_ = eventReader.Close()
		_ = eventWriter.Close()
		_ = controlReader.Close()
		_ = controlWriter.Close()
		return nil, newError(CodeProcess)
	}
	_ = configReader.Close()
	_ = eventWriter.Close()
	_ = controlReader.Close()

	encodeErr := json.NewEncoder(configWriter).Encode(config)
	closeErr := configWriter.Close()
	if encodeErr != nil || closeErr != nil {
		child.abort()
		return nil, newError(CodeProtocol)
	}
	return child, nil
}

func (child *childProcess) expectEvent(want descriptorEvent) error {
	if child == nil || child.eventReader == nil {
		return newError(CodeProtocol)
	}
	var value [1]byte
	if _, err := io.ReadFull(child.eventReader, value[:]); err != nil || descriptorEvent(value[0]) != want {
		return newError(CodeProtocol)
	}
	return nil
}

func (child *childProcess) sendControl(value descriptorControl) error {
	if child == nil || child.controlWriter == nil {
		return newError(CodeProtocol)
	}
	written, err := child.controlWriter.Write([]byte{byte(value)})
	if err != nil || written != 1 {
		return newError(CodeProtocol)
	}
	return nil
}

func (child *childProcess) finish(role workerRole, backend BackendKind) (workerResponse, error) {
	if child == nil || child.command == nil || child.waited {
		return workerResponse{}, newError(CodeProtocol)
	}
	waitErr := child.command.Wait()
	child.waited = true
	_ = child.eventReader.Close()
	_ = child.controlWriter.Close()
	response, decodeErr := decodeWorkerResponse(bytes.NewReader(child.stdout.bytes()))
	if decodeErr != nil || child.stdout.exceeded || child.stderr.exceeded || response.Role != role || response.Backend != backend {
		return workerResponse{}, newError(CodeProtocol)
	}
	if !response.OK {
		return response, newError(response.ErrorCode)
	}
	if waitErr != nil {
		return workerResponse{}, newError(CodeProcess)
	}
	if len(child.stderr.bytes()) != 0 {
		return workerResponse{}, newError(CodeProtocol)
	}
	return response, nil
}

func (child *childProcess) abort() {
	if child == nil {
		return
	}
	if child.eventReader != nil {
		_ = child.eventReader.Close()
	}
	if child.controlWriter != nil {
		_ = child.controlWriter.Close()
	}
	if child.command != nil && !child.waited {
		if child.command.Process != nil {
			_ = child.command.Process.Kill()
		}
		_ = child.command.Wait()
		child.waited = true
	}
}

func countSecretOccurrences(outputs [][]byte, secrets []string) int {
	count := 0
	for _, output := range outputs {
		for _, secret := range secrets {
			if secret != "" {
				count += bytes.Count(output, []byte(secret))
			}
		}
	}
	return count
}

func countSQLiteArtifactSecretOccurrences(database DatabaseConfig, secrets []string) (int, error) {
	if database.state == nil || database.state.kind != BackendSQLite {
		return 0, nil
	}
	count := 0
	for _, path := range []string{
		database.state.sqlitePath,
		database.state.sqlitePath + "-wal",
		database.state.sqlitePath + "-shm",
		database.state.sqlitePath + "-journal",
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, newError(CodePersistence)
		}
		count += countSecretOccurrences([][]byte{content}, secrets)
	}
	return count, nil
}
