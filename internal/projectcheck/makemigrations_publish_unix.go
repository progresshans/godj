//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/progresshans/godj/internal/projectmigration"
	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations/definition"
	"golang.org/x/sys/unix"
)

const (
	makemigrationsTempPrefix       = ".godj-makemigrations-tmp-v1-"
	makemigrationsTempDigestDomain = "godj/migration-temp/v1\x00"
	makemigrationsMaxEmbedMatches  = 1 << 20
)

type makemigrationsPublicationStep string

const (
	makemigrationsStepLockAcquired       makemigrationsPublicationStep = "lock_acquired"
	makemigrationsStepRecoveryScanned    makemigrationsPublicationStep = "recovery_scanned"
	makemigrationsStepSecondSnapshot     makemigrationsPublicationStep = "second_snapshot_verified"
	makemigrationsStepTempCreated        makemigrationsPublicationStep = "temp_created"
	makemigrationsStepTempWriteProgress  makemigrationsPublicationStep = "temp_write_progress"
	makemigrationsStepTempWritten        makemigrationsPublicationStep = "temp_written"
	makemigrationsStepTempFsynced        makemigrationsPublicationStep = "temp_fsynced"
	makemigrationsStepBeforeRename       makemigrationsPublicationStep = "before_each_rename"
	makemigrationsStepRenameReturned     makemigrationsPublicationStep = "rename_returned"
	makemigrationsStepTargetVerified     makemigrationsPublicationStep = "target_verified"
	makemigrationsStepDirectoryFsynced   makemigrationsPublicationStep = "directory_fsynced"
	makemigrationsStepCandidateCommitted makemigrationsPublicationStep = "candidate_committed"
	makemigrationsSyncRecoveryCleanup    makemigrationsPublicationStep = "recovery_cleanup"
	makemigrationsSyncRecoveryAdoption   makemigrationsPublicationStep = "recovery_adoption"
	makemigrationsSyncCandidateCommitted makemigrationsPublicationStep = "candidate_commit"
	makemigrationsSyncTempCleanup        makemigrationsPublicationStep = "temp_cleanup"
)

// makemigrationsPublicationHooks are process-local test seams. Production
// callers always receive the defaults; no hook is part of the public command
// or private project protocol.
type makemigrationsPublicationHooks struct {
	after           func(makemigrationsPublicationStep, string, int) error
	renameNoReplace func(int, string, int, string) error
	syncDirectory   func(*os.File, makemigrationsPublicationStep, int) error
	writeTemp       func(*os.File, []byte, string, int) error
}

func completeMakemigrationsPublicationHooks(hooks makemigrationsPublicationHooks) makemigrationsPublicationHooks {
	if hooks.renameNoReplace == nil {
		hooks.renameNoReplace = makemigrationsRenameNoReplace
	}
	if hooks.syncDirectory == nil {
		hooks.syncDirectory = func(directory *os.File, _ makemigrationsPublicationStep, _ int) error {
			return directory.Sync()
		}
	}
	if hooks.writeTemp == nil {
		hooks.writeTemp = func(file *os.File, document []byte, _ string, _ int) error {
			return writeMakemigrationsAll(file, document)
		}
	}
	return hooks
}

func (hooks makemigrationsPublicationHooks) fire(step makemigrationsPublicationStep, target string, index int) error {
	if hooks.after == nil {
		return nil
	}
	return hooks.after(step, target, index)
}

type makemigrationsWriterRoot struct {
	project      retainedProject
	logical      string
	directory    *os.File
	identity     unix.Stat_t
	lockFile     *os.File
	lockIdentity unix.Stat_t
	lockHeld     bool
}

func retainMakemigrationsWriterRoot(project retainedProject, logical string) (*makemigrationsWriterRoot, *MakemigrationsFailure) {
	directory, identity, err := openMakemigrationsLogicalRoot(project.root, logical)
	if err != nil || !verifyRetainedProject(project) {
		if directory != nil {
			_ = directory.Close()
		}
		candidate := makemigrationsSourceConflict()
		return nil, &candidate
	}
	root := &makemigrationsWriterRoot{project: project, logical: logical, directory: directory, identity: identity}
	if !root.verify(false) {
		_ = root.close()
		candidate := makemigrationsSourceConflict()
		return nil, &candidate
	}
	return root, nil
}

func (root *makemigrationsWriterRoot) close() error {
	if root == nil || root.directory == nil {
		return nil
	}
	err := root.directory.Close()
	root.directory = nil
	root.lockFile = nil
	root.lockHeld = false
	root.lockIdentity = unix.Stat_t{}
	return err
}

func (root *makemigrationsWriterRoot) descriptorMatches() bool {
	if root == nil || root.directory == nil {
		return false
	}
	var retained unix.Stat_t
	return unix.Fstat(int(root.directory.Fd()), &retained) == nil &&
		retained.Mode&unix.S_IFMT == unix.S_IFDIR && sameIdentity(retained, root.identity)
}

func (root *makemigrationsWriterRoot) verify(requireLock bool) bool {
	if !root.descriptorMatches() || !verifyRetainedProject(root.project) ||
		!verifyMakemigrationsLogicalRoot(root.project.root, root.logical, root.identity) {
		return false
	}
	if !requireLock {
		return true
	}
	if !root.lockHeld || root.lockFile == nil || !sameIdentity(root.lockIdentity, root.identity) {
		return false
	}
	var locked unix.Stat_t
	return unix.Fstat(int(root.lockFile.Fd()), &locked) == nil &&
		locked.Mode&unix.S_IFMT == unix.S_IFDIR && sameIdentity(locked, root.lockIdentity)
}

type makemigrationsWriterLock struct {
	file *os.File
	root *makemigrationsWriterRoot
}

func acquireMakemigrationsWriterLock(
	input MakemigrationsInvocation,
	root *makemigrationsWriterRoot,
	hooks makemigrationsPublicationHooks,
	report *MakemigrationsReport,
) (*makemigrationsWriterLock, *MakemigrationsFailure) {
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return nil, terminal
	}
	if !root.verify(false) {
		candidate := makemigrationsSourceConflict()
		return nil, &candidate
	}

	// The retained physical writer directory is itself the dedicated lock
	// object. This keeps a clean normal invocation free of control-file writes
	// while all cooperative writers still serialize on one stable inode.
	file, err := duplicateMakemigrationsDirectory(root.directory)
	if err != nil {
		candidate := makemigrationsPublicationFailed()
		return nil, &candidate
	}
	fd := int(file.Fd())
	fail := func(failure MakemigrationsFailure, cleanupFailed bool) (*makemigrationsWriterLock, *MakemigrationsFailure) {
		cleanupFailed = file.Close() != nil || cleanupFailed
		if cleanupFailed {
			report.CleanupFailed = 1
			combined := combineMakemigrationsCleanup(&failure, true)
			return nil, combined
		}
		return nil, &failure
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || opened.Mode&unix.S_IFMT != unix.S_IFDIR || !sameIdentity(opened, root.identity) {
		return fail(makemigrationsRecoveryRequired(), false)
	}

	for {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			break
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			if terminal := makemigrationsBarrier(input, nil); terminal != nil {
				return fail(*terminal, false)
			}
			return fail(makemigrationsPublicationFailed(), false)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-input.Context.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if terminal := makemigrationsBarrier(input, nil); terminal != nil {
				return fail(*terminal, false)
			}
			return fail(MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectCanceled}, false)
		case <-input.Interrupt:
			if !timer.Stop() {
				<-timer.C
			}
			if terminal := makemigrationsBarrier(input, nil); terminal != nil {
				return fail(*terminal, false)
			}
			return fail(MakemigrationsFailure{Category: MakemigrationsCategoryProcess, Code: MakemigrationsCodeProjectInterrupted}, false)
		case <-timer.C:
		}
	}

	root.lockIdentity = opened
	root.lockFile = file
	root.lockHeld = true
	if !root.verify(true) {
		root.lockHeld = false
		root.lockFile = nil
		root.lockIdentity = unix.Stat_t{}
		unlockFailed := unix.Flock(fd, unix.LOCK_UN) != nil
		return fail(makemigrationsRecoveryRequired(), unlockFailed)
	}
	report.WriterLockAcquisitions++
	if err := hooks.fire(makemigrationsStepLockAcquired, root.logical, -1); err != nil {
		root.lockHeld = false
		root.lockFile = nil
		root.lockIdentity = unix.Stat_t{}
		unlockFailed := unix.Flock(fd, unix.LOCK_UN) != nil
		return fail(makemigrationsPublicationFailed(), unlockFailed)
	}
	return &makemigrationsWriterLock{file: file, root: root}, nil
}

func (lock *makemigrationsWriterLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	if lock.root != nil {
		lock.root.lockHeld = false
		lock.root.lockFile = nil
		lock.root.lockIdentity = unix.Stat_t{}
	}
	err := errors.Join(unix.Flock(int(lock.file.Fd()), unix.LOCK_UN), lock.file.Close())
	lock.file = nil
	lock.root = nil
	return err
}

type makemigrationsRecoveryEntry struct {
	tempName      string
	targetName    string
	document      []byte
	tempIdentity  unix.Stat_t
	targetPresent bool
}

type makemigrationsRecoveryScan struct {
	entries     []makemigrationsRecoveryEntry
	catalog     []definition.Source
	catalogSeal map[string]unix.Stat_t
	ambiguous   bool
}

func inspectMakemigrationsRecovery(
	ctx context.Context,
	root *makemigrationsWriterRoot,
	programmatic []definition.Source,
	report *MakemigrationsReport,
) (makemigrationsRecoveryScan, error) {
	if ctx == nil || root == nil || !root.verify(false) {
		return makemigrationsRecoveryScan{}, errors.New("invalid makemigrations recovery authority")
	}
	reader, err := duplicateMakemigrationsDirectory(root.directory)
	if err != nil {
		return makemigrationsRecoveryScan{}, err
	}
	defer reader.Close()

	result := makemigrationsRecoveryScan{entries: make([]makemigrationsRecoveryEntry, 0, 1)}
	tempNames := make([]string, 0, 1)
	seen := 0
	for {
		if err := ctx.Err(); err != nil {
			clearMakemigrationsRecoveryScan(&result)
			return makemigrationsRecoveryScan{}, err
		}
		entries, readErr := reader.ReadDir(makemigrationsDirectoryChunk)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			clearMakemigrationsRecoveryScan(&result)
			return makemigrationsRecoveryScan{}, readErr
		}
		for _, entry := range entries {
			if seen >= makemigrationsMaxDirectoryEntries {
				clearMakemigrationsRecoveryScan(&result)
				return makemigrationsRecoveryScan{}, errors.New("makemigrations recovery entry limit exceeded")
			}
			seen++
			name := entry.Name()
			if !strings.HasPrefix(name, makemigrationsTempPrefix) {
				continue
			}
			if len(name) != len(makemigrationsTempPrefix)+sha256.Size*2 || !isLowerHex(name[len(makemigrationsTempPrefix):]) {
				result.ambiguous = true
				continue
			}
			var tempStat unix.Stat_t
			if unix.Fstatat(int(root.directory.Fd()), name, &tempStat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
				tempStat.Mode&unix.S_IFMT != unix.S_IFREG || tempStat.Mode&0o7777 != 0o600 ||
				uint64(tempStat.Dev) != uint64(root.identity.Dev) {
				result.ambiguous = true
				continue
			}
			tempNames = append(tempNames, name)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if len(tempNames) > 1 {
		result.ambiguous = true
	}
	if report != nil {
		report.IndependentCatalogSnapshots++
	}
	filesystem, catalogErr := captureMakemigrationsFilesystemCatalog(ctx, root.project, root.logical)
	if catalogErr != nil {
		if err := ctx.Err(); err != nil {
			clearMakemigrationsRecoveryScan(&result)
			return makemigrationsRecoveryScan{}, err
		}
		result.ambiguous = true
	}
	if !result.ambiguous {
		seals, sealErr := sealMakemigrationsCatalogMembers(root, filesystem)
		if sealErr != nil {
			result.ambiguous = true
		} else {
			result.catalog = filesystem
			result.catalogSeal = seals
			filesystem = nil
		}
	}
	defer clearDefinitionSources(filesystem)
	if !result.ambiguous && len(tempNames) == 0 {
		combined := make([]definition.Source, 0, len(result.catalog)+len(programmatic))
		combined = append(combined, result.catalog...)
		combined = append(combined, programmatic...)
		if _, _, loadErr := definition.Load(combined...); loadErr != nil {
			result.ambiguous = true
		}
	}
	if !result.ambiguous && len(tempNames) == 1 {
		recovery, valid := inspectMakemigrationsOwnedTemp(root, tempNames[0], result.catalog, programmatic)
		if !valid {
			result.ambiguous = true
		} else {
			result.entries = append(result.entries, recovery)
		}
	}
	if !root.verify(false) {
		result.ambiguous = true
	}
	return result, nil
}

func inspectMakemigrationsOwnedTemp(
	root *makemigrationsWriterRoot,
	name string,
	filesystem []definition.Source,
	programmatic []definition.Source,
) (makemigrationsRecoveryEntry, bool) {
	document, stat, present, err := readMakemigrationsRegularAt(root.directory, name, definition.MaxDocumentBytes)
	if err != nil || !present || stat.Mode&0o7777 != 0o600 || uint64(stat.Dev) != uint64(root.identity.Dev) {
		clear(document)
		return makemigrationsRecoveryEntry{}, false
	}
	tempSourceID := makemigrationsRecoverySourceID(name, filesystem, programmatic)
	combined := make([]definition.Source, 0, len(filesystem)+len(programmatic)+1)
	combined = append(combined, filesystem...)
	combined = append(combined, programmatic...)
	combined = append(combined, definition.Source{SourceID: tempSourceID, Document: document})
	loaded, _, err := definition.Load(combined...)
	if err != nil {
		clear(document)
		return makemigrationsRecoveryEntry{}, false
	}
	definitions := loaded.Definitions()
	sources := loaded.Sources()
	var owned *definition.SourceInfo
	for index := range sources {
		if sources[index].SourceID == tempSourceID {
			candidate := sources[index]
			owned = &candidate
			break
		}
	}
	if owned == nil || owned.Producer.Name != "godj-makemigrations" || owned.Producer.Version != "1" {
		clear(document)
		return makemigrationsRecoveryEntry{}, false
	}
	var ownedDefinitionFound bool
	for index := range definitions {
		if definitions[index].Key() == owned.Migration {
			ownedDefinitionFound = true
			break
		}
	}
	if !ownedDefinitionFound {
		clear(document)
		return makemigrationsRecoveryEntry{}, false
	}
	target, targetErr := writerprotocol.CandidateTargetBasename(owned.Migration.App, owned.Migration.Name)
	if targetErr != nil || makemigrationsTempBasename(target, document) != name {
		clear(document)
		return makemigrationsRecoveryEntry{}, false
	}
	targetDocument, targetStat, targetPresent, targetErr := readMakemigrationsRegularAt(root.directory, target, definition.MaxDocumentBytes)
	if targetErr != nil || (targetPresent && (targetStat.Mode&0o7777 != 0o600 || !bytes.Equal(targetDocument, document))) {
		clear(targetDocument)
		clear(document)
		return makemigrationsRecoveryEntry{}, false
	}
	clear(targetDocument)
	return makemigrationsRecoveryEntry{
		tempName: name, targetName: target, document: document,
		tempIdentity: stat, targetPresent: targetPresent,
	}, true
}

func makemigrationsRecoverySourceID(name string, filesystem, programmatic []definition.Source) string {
	used := make(map[string]struct{}, len(filesystem)+len(programmatic))
	for _, sources := range [][]definition.Source{filesystem, programmatic} {
		for index := range sources {
			used[sources[index].SourceID] = struct{}{}
		}
	}
	base := ".godj-recovery/" + name
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix != 0 {
			candidate = base + "-" + strconv.Itoa(suffix)
		}
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func clearMakemigrationsRecoveryScan(scan *makemigrationsRecoveryScan) {
	if scan == nil {
		return
	}
	for index := range scan.entries {
		clear(scan.entries[index].document)
	}
	clearDefinitionSources(scan.catalog)
	*scan = makemigrationsRecoveryScan{}
}

func equalMakemigrationsRecoveryScans(left, right makemigrationsRecoveryScan) bool {
	if left.ambiguous != right.ambiguous || len(left.entries) != len(right.entries) ||
		!equalDefinitionSources(left.catalog, right.catalog) ||
		!equalMakemigrationsCatalogSeals(left.catalogSeal, right.catalogSeal) {
		return false
	}
	for index := range left.entries {
		leftEntry := left.entries[index]
		rightEntry := right.entries[index]
		if leftEntry.tempName != rightEntry.tempName || leftEntry.targetName != rightEntry.targetName ||
			leftEntry.targetPresent != rightEntry.targetPresent ||
			!bytes.Equal(leftEntry.document, rightEntry.document) ||
			!sameIdentity(leftEntry.tempIdentity, rightEntry.tempIdentity) ||
			leftEntry.tempIdentity.Mode != rightEntry.tempIdentity.Mode ||
			leftEntry.tempIdentity.Size != rightEntry.tempIdentity.Size {
			return false
		}
	}
	return true
}

func diagnoseMakemigrationsRecovery(
	input MakemigrationsInvocation,
	selected retainedProject,
	snapshot projectmigration.Snapshot,
) *MakemigrationsFailure {
	root, failure := retainMakemigrationsWriterRoot(selected, snapshot.WriterRoot())
	if failure != nil {
		return failure
	}
	programmatic := snapshot.ProgrammaticSources()
	defer clearDefinitionSources(programmatic)
	scan, err := inspectMakemigrationsRecovery(input.Context, root, programmatic, nil)
	closeErr := root.close()
	defer clearMakemigrationsRecoveryScan(&scan)
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if err != nil || closeErr != nil {
		candidate := makemigrationsPublicationFailed()
		return &candidate
	}
	if scan.ambiguous || len(scan.entries) != 0 {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	return nil
}

// preflightMakemigrationsPhysicalPublication is the read-only counterpart of
// normal's locked physical resource check. Dry-run and check run recovery
// diagnosis first, then use this helper without creating a lock, temp, or
// control entry; normal invokes the same resource predicate after recovery.
func preflightMakemigrationsPhysicalPublication(
	input MakemigrationsInvocation,
	selected retainedProject,
	baseline makemigrationsBuildInputFingerprint,
	snapshot projectmigration.Snapshot,
	report *MakemigrationsReport,
) (failure *MakemigrationsFailure) {
	root, retainFailure := retainMakemigrationsWriterRoot(selected, snapshot.WriterRoot())
	if retainFailure != nil {
		return retainFailure
	}
	failure = verifyMakemigrationsPublicationResources(input, baseline, snapshot, root, false)
	if root.close() != nil {
		report.CleanupFailed = 1
		failure = combineMakemigrationsCleanup(failure, true)
	}
	return failure
}

func verifyMakemigrationsPublicationResources(
	input MakemigrationsInvocation,
	baseline makemigrationsBuildInputFingerprint,
	snapshot projectmigration.Snapshot,
	root *makemigrationsWriterRoot,
	requireLock bool,
) *MakemigrationsFailure {
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if root == nil || !root.verify(requireLock) {
		candidate := makemigrationsSourceConflict()
		return &candidate
	}
	candidates := snapshot.Candidates()
	if len(candidates) > writerprotocol.MaxCandidates {
		candidate := MakemigrationsFailure{
			Category: writerprotocol.CategoryCandidate,
			Code:     writerprotocol.CodeCandidateResourceLimitExceeded,
		}
		return &candidate
	}
	for index := range candidates {
		target, err := writerprotocol.CandidateTargetBasename(candidates[index].App(), candidates[index].Name())
		if err != nil {
			candidate := makemigrationsInternalFailure()
			return &candidate
		}
		document := candidates[index].Document()
		temp := makemigrationsTempBasename(target, document)
		clear(document)
		// Besides no-overwrite, these probes give dry-run/check the same
		// NAME_MAX and case-fold collision answer that normal will receive.
		if !makemigrationsTargetAbsent(root.directory, target) || !makemigrationsTargetAbsent(root.directory, temp) {
			if terminal := makemigrationsBarrier(input, nil); terminal != nil {
				return terminal
			}
			candidate := makemigrationsSourceConflict()
			return &candidate
		}
	}
	if makemigrationsCandidatesOverlapEmbedPatterns(baseline, snapshot.WriterRoot(), candidates) {
		if terminal := makemigrationsBarrier(input, nil); terminal != nil {
			return terminal
		}
		candidate := makemigrationsPublicationFailed()
		return &candidate
	}
	directoryEntries, err := countMakemigrationsWriterDirectoryEntries(root.directory)
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if err != nil {
		candidate := makemigrationsPublicationFailed()
		return &candidate
	}
	if !makemigrationsDirectoryHasHeadroom(directoryEntries, len(candidates)) {
		candidate := MakemigrationsFailure{
			Category: writerprotocol.CategoryCandidate,
			Code:     writerprotocol.CodeCandidateResourceLimitExceeded,
		}
		return &candidate
	}
	if !root.verify(requireLock) {
		candidate := makemigrationsSourceConflict()
		return &candidate
	}
	return nil
}

func recoverMakemigrationsPublication(
	input MakemigrationsInvocation,
	root *makemigrationsWriterRoot,
	programmatic []definition.Source,
	hooks makemigrationsPublicationHooks,
	report *MakemigrationsReport,
) *MakemigrationsFailure {
	if !root.verify(true) {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	scan, err := inspectMakemigrationsRecovery(input.Context, root, programmatic, report)
	defer clearMakemigrationsRecoveryScan(&scan)
	if err != nil {
		if terminal := makemigrationsBarrier(input, nil); terminal != nil {
			return terminal
		}
		candidate := makemigrationsPublicationFailed()
		return &candidate
	}
	if err := hooks.fire(makemigrationsStepRecoveryScanned, root.logical, -1); err != nil {
		candidate := makemigrationsPublicationFailed()
		return &candidate
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if scan.ambiguous || (len(scan.entries) == 1 && scan.entries[0].targetPresent) {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	// The recovery hook is deliberately before this second scan. Recovery is
	// allowed to remove a temp only when two complete scans agree exactly. The
	// directory lock excludes cooperative writers; POSIX has no inode-conditioned
	// unlink primitive, so a non-cooperative local actor remains out of scope.
	confirmed, confirmErr := inspectMakemigrationsRecovery(input.Context, root, programmatic, report)
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		clearMakemigrationsRecoveryScan(&confirmed)
		return terminal
	}
	if confirmErr != nil || !equalMakemigrationsRecoveryScans(scan, confirmed) || !root.verify(true) {
		clearMakemigrationsRecoveryScan(&confirmed)
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	clearMakemigrationsRecoveryScan(&scan)
	scan = confirmed
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if len(scan.entries) == 1 {
		entry := scan.entries[0]
		if !verifyMakemigrationsOwnedTempBeforeCleanup(
			root, entry.tempName, entry.targetName, entry.tempIdentity, entry.document, true,
		) || !makemigrationsReservedTempRosterIsExactly(root.directory, entry.tempName) ||
			!makemigrationsTargetAbsent(root.directory, entry.targetName) {
			candidate := makemigrationsRecoveryRequired()
			return &candidate
		}
		if terminal := makemigrationsBarrier(input, nil); terminal != nil {
			return terminal
		}
		if unix.Unlinkat(int(root.directory.Fd()), entry.tempName, 0) != nil {
			candidate := makemigrationsRecoveryRequired()
			return &candidate
		}
		if err := syncMakemigrationsWriterDirectory(hooks, root.directory, makemigrationsSyncRecoveryCleanup, -1, report); err != nil {
			candidate := makemigrationsRecoveryRequired()
			return &candidate
		}
		report.OwnedTempRecoveries++
	} else if err := syncMakemigrationsWriterDirectory(hooks, root.directory, makemigrationsSyncRecoveryAdoption, -1, report); err != nil {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	post, postErr := inspectMakemigrationsRecovery(input.Context, root, programmatic, report)
	postMatches := postErr == nil && !post.ambiguous && len(post.entries) == 0 &&
		equalDefinitionSources(scan.catalog, post.catalog) &&
		equalMakemigrationsCatalogSeals(scan.catalogSeal, post.catalogSeal) && root.verify(true)
	clearMakemigrationsRecoveryScan(&post)
	if !postMatches {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	return nil
}

type makemigrationsIncompleteTempScan struct {
	tempName     string
	targetName   string
	prefix       []byte
	document     []byte
	tempIdentity unix.Stat_t
	catalog      []definition.Source
	catalogSeal  map[string]unix.Stat_t
}

func recoverMakemigrationsIncompleteTemp(
	input MakemigrationsInvocation,
	selected retainedProject,
	environment []string,
	root *makemigrationsWriterRoot,
	baseline makemigrationsBuildInputFingerprint,
	fresh projectmigration.Snapshot,
	hooks makemigrationsPublicationHooks,
	report *MakemigrationsReport,
) *MakemigrationsFailure {
	first, ok := inspectMakemigrationsIncompleteTemp(input, root, fresh, report)
	defer clearMakemigrationsIncompleteTempScan(&first)
	if !ok {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	second, confirmed := inspectMakemigrationsIncompleteTemp(input, root, fresh, report)
	defer clearMakemigrationsIncompleteTempScan(&second)
	if !confirmed || !equalMakemigrationsIncompleteTempScans(first, second) || !root.verify(true) {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if failure := verifyMakemigrationsPublicationCAS(
		input, selected, environment, root, baseline, second.catalog, second.catalogSeal, second.tempName, report,
	); failure != nil {
		return failure
	}
	if !verifyMakemigrationsIncompleteTempBeforeCleanup(root, second) {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if unix.Unlinkat(int(root.directory.Fd()), second.tempName, 0) != nil {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if err := syncMakemigrationsWriterDirectory(hooks, root.directory, makemigrationsSyncRecoveryCleanup, -1, report); err != nil {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	report.OwnedTempRecoveries++
	if failure := verifyMakemigrationsPublicationCAS(
		input, selected, environment, root, baseline, second.catalog, second.catalogSeal, "", report,
	); failure != nil {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	return nil
}

func inspectMakemigrationsIncompleteTemp(
	input MakemigrationsInvocation,
	root *makemigrationsWriterRoot,
	fresh projectmigration.Snapshot,
	report *MakemigrationsReport,
) (makemigrationsIncompleteTempScan, bool) {
	ctx := input.Context
	if ctx == nil || root == nil || !root.verify(true) || ctx.Err() != nil {
		return makemigrationsIncompleteTempScan{}, false
	}
	tempName, ok := singleMakemigrationsReservedTempName(root.directory)
	if !ok {
		return makemigrationsIncompleteTempScan{}, false
	}
	targetName, document, planned := plannedMakemigrationsTempDocument(fresh, tempName)
	if !planned {
		return makemigrationsIncompleteTempScan{}, false
	}
	fail := func(prefix []byte, catalog []definition.Source) (makemigrationsIncompleteTempScan, bool) {
		clear(prefix)
		clear(document)
		clearDefinitionSources(catalog)
		return makemigrationsIncompleteTempScan{}, false
	}
	prefix, identity, present, err := readMakemigrationsRegularAt(root.directory, tempName, definition.MaxDocumentBytes)
	if err != nil || !present || identity.Mode&0o7777 != 0o600 || uint64(identity.Dev) != uint64(root.identity.Dev) ||
		len(prefix) >= len(document) || !bytes.HasPrefix(document, prefix) ||
		!makemigrationsTargetAbsent(root.directory, targetName) || !root.verify(true) {
		return fail(prefix, nil)
	}
	catalog, seals, catalogErr := captureMakemigrationsSealedCatalog(input, root, report)
	if catalogErr != nil {
		return fail(prefix, catalog)
	}
	expected := fresh.FilesystemSources()
	catalogMatches := equalDefinitionSources(expected, catalog)
	clearDefinitionSources(expected)
	if !catalogMatches || ctx.Err() != nil || !root.verify(true) ||
		!makemigrationsReservedTempRosterIsExactly(root.directory, tempName) {
		return fail(prefix, catalog)
	}
	return makemigrationsIncompleteTempScan{
		tempName: tempName, targetName: targetName, prefix: prefix, document: document,
		tempIdentity: identity, catalog: catalog, catalogSeal: seals,
	}, true
}

func plannedMakemigrationsTempDocument(
	snapshot projectmigration.Snapshot,
	tempName string,
) (string, []byte, bool) {
	var matchedTarget string
	var matchedDocument []byte
	matches := 0
	for _, candidate := range snapshot.Candidates() {
		target, err := writerprotocol.CandidateTargetBasename(candidate.App(), candidate.Name())
		document := candidate.Document()
		if err == nil && makemigrationsTempBasename(target, document) == tempName {
			matches++
			if matches == 1 {
				matchedTarget = target
				matchedDocument = append([]byte(nil), document...)
			}
		}
		clear(document)
	}
	if matches != 1 {
		clear(matchedDocument)
		return "", nil, false
	}
	return matchedTarget, matchedDocument, true
}

func singleMakemigrationsReservedTempName(directory *os.File) (string, bool) {
	reader, err := duplicateMakemigrationsDirectory(directory)
	if err != nil {
		return "", false
	}
	defer reader.Close()
	name := ""
	seen := 0
	for {
		entries, readErr := reader.ReadDir(makemigrationsDirectoryChunk)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return "", false
		}
		for _, entry := range entries {
			if seen >= makemigrationsMaxDirectoryEntries {
				return "", false
			}
			seen++
			if !strings.HasPrefix(entry.Name(), makemigrationsTempPrefix) {
				continue
			}
			if name != "" || len(entry.Name()) != len(makemigrationsTempPrefix)+sha256.Size*2 ||
				!isLowerHex(entry.Name()[len(makemigrationsTempPrefix):]) {
				return "", false
			}
			name = entry.Name()
		}
		if errors.Is(readErr, io.EOF) {
			return name, name != ""
		}
	}
}

func equalMakemigrationsIncompleteTempScans(left, right makemigrationsIncompleteTempScan) bool {
	return left.tempName == right.tempName && left.targetName == right.targetName &&
		bytes.Equal(left.prefix, right.prefix) && bytes.Equal(left.document, right.document) &&
		sameIdentity(left.tempIdentity, right.tempIdentity) &&
		left.tempIdentity.Mode == right.tempIdentity.Mode && left.tempIdentity.Size == right.tempIdentity.Size &&
		equalDefinitionSources(left.catalog, right.catalog) &&
		equalMakemigrationsCatalogSeals(left.catalogSeal, right.catalogSeal)
}

func verifyMakemigrationsIncompleteTempBeforeCleanup(
	root *makemigrationsWriterRoot,
	scan makemigrationsIncompleteTempScan,
) bool {
	actual, identity, present, err := readMakemigrationsRegularAt(root.directory, scan.tempName, definition.MaxDocumentBytes)
	defer clear(actual)
	return err == nil && present && root.verify(true) && sameIdentity(identity, scan.tempIdentity) &&
		identity.Mode == scan.tempIdentity.Mode && identity.Size == scan.tempIdentity.Size &&
		identity.Mode&0o7777 == 0o600 && uint64(identity.Dev) == uint64(root.identity.Dev) &&
		bytes.Equal(actual, scan.prefix) && len(actual) < len(scan.document) && bytes.HasPrefix(scan.document, actual) &&
		makemigrationsReservedTempRosterIsExactly(root.directory, scan.tempName) &&
		makemigrationsTargetAbsent(root.directory, scan.targetName)
}

func clearMakemigrationsIncompleteTempScan(scan *makemigrationsIncompleteTempScan) {
	if scan == nil {
		return
	}
	clear(scan.prefix)
	clear(scan.document)
	clearDefinitionSources(scan.catalog)
	*scan = makemigrationsIncompleteTempScan{}
}

func publishMakemigrationsNormal(
	input MakemigrationsInvocation,
	selected retainedProject,
	environment []string,
	runnerCommand Command,
	baseline makemigrationsBuildInputFingerprint,
	initial projectmigration.Snapshot,
	report *MakemigrationsReport,
) (projectmigration.Snapshot, *MakemigrationsFailure) {
	hooks := completeMakemigrationsPublicationHooks(input.publication)
	root, failure := retainMakemigrationsWriterRoot(selected, initial.WriterRoot())
	if failure != nil {
		return projectmigration.Snapshot{}, failure
	}
	lock, failure := acquireMakemigrationsWriterLock(input, root, hooks, report)
	if failure != nil {
		if root.close() != nil {
			report.CleanupFailed = 1
			failure = combineMakemigrationsCleanup(failure, true)
		}
		return projectmigration.Snapshot{}, failure
	}
	finish := func(snapshot projectmigration.Snapshot, primary *MakemigrationsFailure) (projectmigration.Snapshot, *MakemigrationsFailure) {
		cleanupFailed := lock.release() != nil
		cleanupFailed = root.close() != nil || cleanupFailed
		if cleanupFailed {
			report.CleanupFailed = 1
			primary = combineMakemigrationsCleanup(primary, true)
		}
		return snapshot, primary
	}

	preRecoveryBuild, preRecoveryFailure := captureMakemigrationsBuildInput(input, selected, environment, report)
	preRecoveryFailure = normalizeMakemigrationsCASFailure(makemigrationsBarrier(input, preRecoveryFailure))
	if preRecoveryFailure != nil || !equalMakemigrationsBuildInputFingerprint(preRecoveryBuild, baseline) || !root.verify(true) {
		if preRecoveryFailure == nil {
			candidate := makemigrationsSourceConflict()
			preRecoveryFailure = &candidate
		}
		return finish(projectmigration.Snapshot{}, preRecoveryFailure)
	}
	programmatic := initial.ProgrammaticSources()
	defer clearDefinitionSources(programmatic)
	loadFreshPlan := func() (projectmigration.Snapshot, *MakemigrationsFailure) {
		response, runnerFailure := executeMakemigrationsRunner(input, runnerCommand, report)
		if runnerFailure != nil {
			return projectmigration.Snapshot{}, runnerFailure
		}
		fresh, verifyFailure := independentlyVerifyMakemigrationsResponse(input, selected, response, report)
		clearMakemigrationsProtocolResult(&response)
		if verifyFailure != nil {
			return projectmigration.Snapshot{}, verifyFailure
		}
		if !sameMakemigrationsCompiledAuthority(initial, fresh) || fresh.WriterRoot() != root.logical || !root.verify(true) {
			candidate := makemigrationsSourceConflict()
			return projectmigration.Snapshot{}, &candidate
		}
		return fresh, nil
	}
	fresh, failure := loadFreshPlan()
	if failure != nil {
		return finish(projectmigration.Snapshot{}, failure)
	}
	if err := hooks.fire(makemigrationsStepSecondSnapshot, root.logical, -1); err != nil {
		candidate := makemigrationsPublicationFailed()
		return finish(projectmigration.Snapshot{}, &candidate)
	}
	if recoveryFailure := recoverMakemigrationsPublication(input, root, programmatic, hooks, report); recoveryFailure != nil {
		// An empty or partially written temp cannot authenticate itself by
		// definition decoding. Only a fresh runner plan captured under the
		// writer lock may authorize cleanup of that exact deterministic temp.
		if *recoveryFailure != makemigrationsRecoveryRequired() {
			return finish(projectmigration.Snapshot{}, recoveryFailure)
		}
		if failure = recoverMakemigrationsIncompleteTemp(
			input, selected, environment, root, baseline, fresh, hooks, report,
		); failure != nil {
			return finish(projectmigration.Snapshot{}, failure)
		}
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return finish(projectmigration.Snapshot{}, terminal)
	}
	postRecoveryBuild, postRecoveryFailure := captureMakemigrationsBuildInput(input, selected, environment, report)
	postRecoveryFailure = normalizeMakemigrationsCASFailure(makemigrationsBarrier(input, postRecoveryFailure))
	if postRecoveryFailure != nil || !equalMakemigrationsBuildInputFingerprint(postRecoveryBuild, baseline) || !root.verify(true) {
		if postRecoveryFailure == nil {
			candidate := makemigrationsSourceConflict()
			postRecoveryFailure = &candidate
		}
		return finish(projectmigration.Snapshot{}, postRecoveryFailure)
	}
	if failure := verifyMakemigrationsPublicationResources(input, baseline, fresh, root, true); failure != nil {
		return finish(projectmigration.Snapshot{}, failure)
	}
	candidates := fresh.Candidates()

	expected := fresh.FilesystemSources()
	defer clearDefinitionSources(expected)
	current, expectedSeals, sealErr := captureMakemigrationsSealedCatalog(input, root, report)
	initialCatalogMatches := sealErr == nil && equalDefinitionSources(expected, current)
	clearDefinitionSources(current)
	if !initialCatalogMatches {
		if terminal := makemigrationsBarrier(input, nil); terminal != nil {
			return finish(projectmigration.Snapshot{}, terminal)
		}
		candidate := makemigrationsSourceConflict()
		return finish(projectmigration.Snapshot{}, &candidate)
	}
	if failure := verifyMakemigrationsPublicationCAS(input, selected, environment, root, baseline, expected, expectedSeals, "", report); failure != nil {
		return finish(projectmigration.Snapshot{}, failure)
	}

	for index := range candidates {
		if terminal := makemigrationsBarrier(input, nil); terminal != nil {
			return finish(projectmigration.Snapshot{}, makemigrationsFailureAfterPrefix(*terminal, report.PublishedCandidates))
		}
		document := candidates[index].Document()
		target, targetErr := writerprotocol.CandidateTargetBasename(candidates[index].App(), candidates[index].Name())
		if targetErr != nil {
			clear(document)
			candidate := makemigrationsInternalFailure()
			return finish(projectmigration.Snapshot{}, &candidate)
		}
		var committedIdentity unix.Stat_t
		failure := publishMakemigrationsCandidate(
			input, root, hooks, target, document, index, report, &committedIdentity,
			func() *MakemigrationsFailure {
				return verifyMakemigrationsPublicationCAS(
					input, selected, environment, root, baseline, expected, expectedSeals,
					makemigrationsTempBasename(target, document), report,
				)
			},
		)
		if failure != nil {
			clear(document)
			return finish(projectmigration.Snapshot{}, makemigrationsFailureAfterPrefix(*failure, report.PublishedCandidates))
		}
		sourceID := target
		if fresh.WriterRoot() != "." {
			sourceID = path.Join(fresh.WriterRoot(), target)
		}
		expected = append(expected, definition.Source{SourceID: sourceID, Document: append([]byte(nil), document...)})
		expectedSeals[sourceID] = committedIdentity
		sort.Slice(expected, func(left, right int) bool {
			return bytes.Compare([]byte(expected[left].SourceID), []byte(expected[right].SourceID)) < 0
		})
		clear(document)
		if err := hooks.fire(makemigrationsStepCandidateCommitted, target, index); err != nil {
			candidate := makemigrationsRecoveryRequired()
			return finish(projectmigration.Snapshot{}, &candidate)
		}
	}

	if failure := verifyMakemigrationsPublicationCAS(input, selected, environment, root, baseline, expected, expectedSeals, "", report); failure != nil {
		return finish(projectmigration.Snapshot{}, makemigrationsFailureAfterPrefix(*failure, report.PublishedCandidates))
	}
	return finish(fresh, nil)
}

func makemigrationsCandidatesOverlapEmbedPatterns(
	build makemigrationsBuildInputFingerprint,
	writerRoot string,
	candidates []projectmigration.Candidate,
) bool {
	if len(build.embedPatterns) == 0 || len(candidates) == 0 {
		return false
	}
	comparisons := 0
	for index := range candidates {
		target, err := writerprotocol.CandidateTargetBasename(candidates[index].App(), candidates[index].Name())
		if err != nil {
			return true
		}
		document := candidates[index].Document()
		temp := makemigrationsTempBasename(target, document)
		clear(document)
		for _, basename := range []string{target, temp} {
			projectPath := basename
			if writerRoot != "." {
				projectPath = path.Join(writerRoot, basename)
			}
			for _, embedded := range build.embedPatterns {
				matched, used := makemigrationsProjectPathMatchesEmbedPattern(projectPath, embedded)
				if used > makemigrationsMaxEmbedMatches-comparisons {
					return true
				}
				comparisons += used
				if matched {
					return true
				}
			}
		}
	}
	return false
}

func makemigrationsProjectPathMatchesEmbedPattern(projectPath string, embedded makemigrationsEmbedPattern) (bool, int) {
	relative := projectPath
	if embedded.packageDirectory != "." {
		prefix := embedded.packageDirectory + "/"
		if !strings.HasPrefix(projectPath, prefix) {
			return false, 1
		}
		relative = strings.TrimPrefix(projectPath, prefix)
	}
	pattern := strings.TrimPrefix(embedded.pattern, "all:")
	comparisons := 0
	for candidate := relative; candidate != "." && candidate != ""; candidate = path.Dir(candidate) {
		comparisons++
		matched, err := path.Match(pattern, candidate)
		if err != nil {
			return true, comparisons
		}
		if matched {
			return true, comparisons
		}
	}
	return false, comparisons
}

func publishMakemigrationsCandidate(
	input MakemigrationsInvocation,
	root *makemigrationsWriterRoot,
	hooks makemigrationsPublicationHooks,
	target string,
	document []byte,
	index int,
	report *MakemigrationsReport,
	committedIdentity *unix.Stat_t,
	beforeRename func() *MakemigrationsFailure,
) *MakemigrationsFailure {
	if len([]byte(target)) > writerprotocol.MaxWriterBasenameBytes || !root.verify(true) {
		candidate := makemigrationsSourceConflict()
		return &candidate
	}
	if _, _, present, err := readMakemigrationsRegularAt(root.directory, target, definition.MaxDocumentBytes); err != nil || present {
		candidate := makemigrationsSourceConflict()
		return &candidate
	}
	temp := makemigrationsTempBasename(target, document)
	if !makemigrationsReservedTempRosterIsExactly(root.directory, "") {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	fd, err := unix.Openat(
		int(root.directory.Fd()), temp,
		unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK,
		0o600,
	)
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			candidate := makemigrationsRecoveryRequired()
			return &candidate
		}
		candidate := makemigrationsPublicationFailed()
		return &candidate
	}
	file := os.NewFile(uintptr(fd), temp)
	if file == nil {
		var created unix.Stat_t
		statErr := unix.Fstat(fd, &created)
		_ = unix.Close(fd)
		candidate := makemigrationsRecoveryRequired()
		if statErr == nil && created.Mode&unix.S_IFMT == unix.S_IFREG && root.verify(true) &&
			makemigrationsEntryIdentityMatches(root.directory, temp, created) &&
			unix.Unlinkat(int(root.directory.Fd()), temp, 0) == nil &&
			syncMakemigrationsWriterDirectory(hooks, root.directory, makemigrationsSyncTempCleanup, index, report) == nil {
			candidate = makemigrationsPublicationFailed()
		}
		return &candidate
	}
	var tempIdentity unix.Stat_t
	if err := unix.Fstat(fd, &tempIdentity); err != nil || tempIdentity.Mode&unix.S_IFMT != unix.S_IFREG ||
		uint64(tempIdentity.Dev) != uint64(root.identity.Dev) {
		_ = file.Close()
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	cleanupBeforeRename := func(primary MakemigrationsFailure, requireExact bool) *MakemigrationsFailure {
		reservedWasOwnedOnly := makemigrationsReservedTempRosterIsExactly(root.directory, temp)
		if !verifyMakemigrationsOwnedTempBeforeCleanup(root, temp, target, tempIdentity, document, requireExact) ||
			!verifyMakemigrationsRetainedTempForCleanup(file, tempIdentity, document, requireExact) {
			_ = file.Close()
			candidate := makemigrationsRecoveryRequired()
			return &candidate
		}
		if unix.Unlinkat(int(root.directory.Fd()), temp, 0) != nil {
			_ = file.Close()
			candidate := makemigrationsRecoveryRequired()
			return &candidate
		}
		var unlinked unix.Stat_t
		retainedUnlinked := unix.Fstat(fd, &unlinked) == nil && sameIdentity(unlinked, tempIdentity) && unlinked.Nlink == 0
		syncErr := syncMakemigrationsWriterDirectory(hooks, root.directory, makemigrationsSyncTempCleanup, index, report)
		closeErr := file.Close()
		if !retainedUnlinked || syncErr != nil {
			candidate := makemigrationsRecoveryRequired()
			return &candidate
		}
		if !reservedWasOwnedOnly || !root.verify(true) || !makemigrationsTargetAbsent(root.directory, temp) ||
			!makemigrationsReservedTempRosterIsExactly(root.directory, "") {
			candidate := makemigrationsRecoveryRequired()
			return &candidate
		}
		if closeErr != nil {
			candidate := makemigrationsCleanupFailure()
			return &candidate
		}
		return &primary
	}
	if err := file.Chmod(0o600); err != nil {
		return cleanupBeforeRename(makemigrationsPublicationFailed(), false)
	}
	var normalizedIdentity unix.Stat_t
	if err := unix.Fstat(fd, &normalizedIdentity); err != nil || normalizedIdentity.Mode&unix.S_IFMT != unix.S_IFREG ||
		normalizedIdentity.Mode&0o7777 != 0o600 || !sameIdentity(normalizedIdentity, tempIdentity) {
		return cleanupBeforeRename(makemigrationsRecoveryRequired(), false)
	}
	tempIdentity = normalizedIdentity
	if err := hooks.fire(makemigrationsStepTempCreated, target, index); err != nil {
		return cleanupBeforeRename(makemigrationsPublicationFailed(), false)
	}
	if err := hooks.writeTemp(file, document, target, index); err != nil {
		return cleanupBeforeRename(makemigrationsPublicationFailed(), false)
	}
	if err := hooks.fire(makemigrationsStepTempWritten, target, index); err != nil {
		return cleanupBeforeRename(makemigrationsPublicationFailed(), true)
	}
	if err := file.Sync(); err != nil {
		return cleanupBeforeRename(makemigrationsPublicationFailed(), true)
	}
	verifiedIdentity, err := verifyMakemigrationsOpenTemp(file, tempIdentity, document)
	if err != nil {
		return cleanupBeforeRename(makemigrationsRecoveryRequired(), true)
	}
	tempIdentity = verifiedIdentity
	if err := hooks.fire(makemigrationsStepTempFsynced, target, index); err != nil {
		return cleanupBeforeRename(makemigrationsPublicationFailed(), true)
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return cleanupBeforeRename(*terminal, true)
	}
	if !root.verify(true) {
		return cleanupBeforeRename(makemigrationsSourceConflict(), true)
	}
	if err := hooks.fire(makemigrationsStepBeforeRename, target, index); err != nil {
		return cleanupBeforeRename(makemigrationsPublicationFailed(), true)
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return cleanupBeforeRename(*terminal, true)
	}
	if !verifyMakemigrationsOwnedTempBeforeCleanup(root, temp, target, tempIdentity, document, true) ||
		!makemigrationsReservedTempRosterIsExactly(root.directory, temp) ||
		!makemigrationsTargetAbsent(root.directory, target) {
		return cleanupBeforeRename(makemigrationsSourceConflict(), true)
	}
	if beforeRename == nil {
		return cleanupBeforeRename(makemigrationsInternalFailure(), true)
	}
	if failure := beforeRename(); failure != nil {
		return cleanupBeforeRename(*failure, true)
	}
	if !verifyMakemigrationsOwnedTempBeforeCleanup(root, temp, target, tempIdentity, document, true) ||
		!makemigrationsReservedTempRosterIsExactly(root.directory, temp) ||
		!makemigrationsTargetAbsent(root.directory, target) {
		return cleanupBeforeRename(makemigrationsSourceConflict(), true)
	}
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return cleanupBeforeRename(*terminal, true)
	}
	if err := hooks.renameNoReplace(int(root.directory.Fd()), temp, int(root.directory.Fd()), target); err != nil {
		primary := makemigrationsPublicationFailed()
		if errors.Is(err, unix.EEXIST) {
			primary = makemigrationsSourceConflict()
		}
		return cleanupBeforeRename(primary, true)
	}
	report.PublicationRenames++
	if err := hooks.fire(makemigrationsStepRenameReturned, target, index); err != nil {
		_ = file.Close()
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	// Once rename succeeds, caller cancellation cannot turn a visible target
	// into a definite-not-published result. Complete verification and the
	// directory durability attempt without consulting the barrier.
	if err := verifyMakemigrationsPublishedTarget(root, target, tempIdentity, document); err != nil {
		_ = file.Close()
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if err := hooks.fire(makemigrationsStepTargetVerified, target, index); err != nil {
		_ = file.Close()
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if err := file.Close(); err != nil {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if err := syncMakemigrationsWriterDirectory(hooks, root.directory, makemigrationsSyncCandidateCommitted, index, report); err != nil {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	report.PublishedCandidates++
	if committedIdentity != nil {
		*committedIdentity = tempIdentity
	}
	if err := hooks.fire(makemigrationsStepDirectoryFsynced, target, index); err != nil {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	if !root.verify(true) {
		candidate := makemigrationsRecoveryRequired()
		return &candidate
	}
	return nil
}

func verifyMakemigrationsRetainedTempForCleanup(
	file *os.File,
	expected unix.Stat_t,
	document []byte,
	requireExact bool,
) bool {
	if file == nil {
		return false
	}
	var current unix.Stat_t
	if unix.Fstat(int(file.Fd()), &current) != nil || current.Mode&unix.S_IFMT != unix.S_IFREG ||
		!sameIdentity(current, expected) {
		return false
	}
	if !requireExact {
		return true
	}
	verified, err := verifyMakemigrationsOpenTemp(file, expected, document)
	return err == nil && sameIdentity(verified, expected)
}

func verifyMakemigrationsPublicationCAS(
	input MakemigrationsInvocation,
	selected retainedProject,
	environment []string,
	root *makemigrationsWriterRoot,
	baseline makemigrationsBuildInputFingerprint,
	expected []definition.Source,
	expectedSeals map[string]unix.Stat_t,
	expectedReservedTemp string,
	report *MakemigrationsReport,
) *MakemigrationsFailure {
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if !root.verify(true) {
		candidate := makemigrationsSourceConflict()
		return &candidate
	}
	currentBuild, failure := captureMakemigrationsBuildInput(input, selected, environment, report)
	failure = normalizeMakemigrationsCASFailure(makemigrationsBarrier(input, failure))
	if failure != nil {
		return failure
	}
	if !equalMakemigrationsBuildInputFingerprint(currentBuild, baseline) {
		candidate := makemigrationsSourceConflict()
		return &candidate
	}
	current, currentSeals, err := captureMakemigrationsSealedCatalog(input, root, report)
	matches := err == nil && equalDefinitionSources(expected, current) &&
		equalMakemigrationsCatalogSeals(expectedSeals, currentSeals) &&
		makemigrationsReservedTempRosterIsExactly(root.directory, expectedReservedTemp) && root.verify(true)
	clearDefinitionSources(current)
	if terminal := makemigrationsBarrier(input, nil); terminal != nil {
		return terminal
	}
	if !matches {
		candidate := makemigrationsSourceConflict()
		return &candidate
	}
	return nil
}

func captureMakemigrationsSealedCatalog(
	input MakemigrationsInvocation,
	root *makemigrationsWriterRoot,
	report *MakemigrationsReport,
) ([]definition.Source, map[string]unix.Stat_t, error) {
	if root == nil || !root.verify(true) {
		return nil, nil, errors.New("makemigrations sealed catalog authority changed")
	}
	report.IndependentCatalogSnapshots++
	first, err := captureMakemigrationsFilesystemCatalog(input.Context, root.project, root.logical)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) ([]definition.Source, map[string]unix.Stat_t, error) {
		clearDefinitionSources(first)
		return nil, nil, err
	}
	seals, err := sealMakemigrationsCatalogMembers(root, first)
	if err != nil {
		return fail(err)
	}
	report.IndependentCatalogSnapshots++
	second, err := captureMakemigrationsFilesystemCatalog(input.Context, root.project, root.logical)
	secondSeals, sealErr := sealMakemigrationsCatalogMembers(root, second)
	matches := err == nil && sealErr == nil && equalDefinitionSources(first, second) &&
		equalMakemigrationsCatalogSeals(seals, secondSeals) && root.verify(true)
	clearDefinitionSources(second)
	if !matches {
		return fail(errors.New("makemigrations sealed catalog changed while captured"))
	}
	return first, seals, nil
}

func sealMakemigrationsCatalogMembers(
	root *makemigrationsWriterRoot,
	sources []definition.Source,
) (map[string]unix.Stat_t, error) {
	seals := make(map[string]unix.Stat_t, len(sources))
	for index := range sources {
		basename, ok := makemigrationsCatalogBasename(root.logical, sources[index].SourceID)
		if !ok {
			return nil, errors.New("makemigrations sealed catalog source identity is not flat")
		}
		document, stat, present, err := readMakemigrationsRegularAt(root.directory, basename, definition.MaxDocumentBytes)
		matches := err == nil && present && bytes.Equal(document, sources[index].Document)
		clear(document)
		if !matches {
			return nil, errors.New("makemigrations sealed catalog member changed")
		}
		seals[sources[index].SourceID] = stat
	}
	return seals, nil
}

func makemigrationsCatalogBasename(logicalRoot, sourceID string) (string, bool) {
	basename := sourceID
	if logicalRoot != "." {
		prefix := logicalRoot + "/"
		if !strings.HasPrefix(sourceID, prefix) {
			return "", false
		}
		basename = strings.TrimPrefix(sourceID, prefix)
	}
	return basename, basename != "" && path.Base(basename) == basename &&
		strings.HasSuffix(basename, ".godj.json") && len([]byte(basename)) <= writerprotocol.MaxWriterBasenameBytes
}

func equalMakemigrationsCatalogSeals(left, right map[string]unix.Stat_t) bool {
	if len(left) != len(right) {
		return false
	}
	for sourceID, leftStat := range left {
		rightStat, exists := right[sourceID]
		if !exists || !sameIdentity(leftStat, rightStat) || leftStat.Mode != rightStat.Mode || leftStat.Size != rightStat.Size {
			return false
		}
	}
	return true
}

func sameMakemigrationsCompiledAuthority(left, right projectmigration.Snapshot) bool {
	leftProgrammatic := left.ProgrammaticSources()
	rightProgrammatic := right.ProgrammaticSources()
	equalProgrammatic := equalDefinitionSources(leftProgrammatic, rightProgrammatic)
	clearDefinitionSources(leftProgrammatic)
	clearDefinitionSources(rightProgrammatic)
	return left.Initialized() && right.Initialized() && equalProgrammatic &&
		left.WriterRoot() == right.WriterRoot() &&
		reflect.DeepEqual(left.ProjectSpec(), right.ProjectSpec()) &&
		left.ProjectSpecDigest() == right.ProjectSpecDigest() &&
		left.GeneratedBundleSnapshotSHA256() == right.GeneratedBundleSnapshotSHA256() &&
		left.ProgrammaticCatalogDigest() == right.ProgrammaticCatalogDigest()
}

func makemigrationsFailureAfterPrefix(failure MakemigrationsFailure, committed int) *MakemigrationsFailure {
	if committed == 0 || failure == makemigrationsRecoveryRequired() {
		candidate := failure
		return &candidate
	}
	candidate := makemigrationsRecoveryRequired()
	return &candidate
}

func makemigrationsPublicationFailed() MakemigrationsFailure {
	return MakemigrationsFailure{Category: MakemigrationsCategoryPublication, Code: MakemigrationsCodePublicationFailed}
}

func makemigrationsRecoveryRequired() MakemigrationsFailure {
	return MakemigrationsFailure{Category: MakemigrationsCategoryPublication, Code: MakemigrationsCodePublicationRecoveryRequired}
}

func makemigrationsTempBasename(target string, document []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(makemigrationsTempDigestDomain))
	var framed [8]byte
	binary.BigEndian.PutUint64(framed[:], uint64(len([]byte(target))))
	_, _ = digest.Write(framed[:])
	_, _ = digest.Write([]byte(target))
	binary.BigEndian.PutUint64(framed[:], uint64(len(document)))
	_, _ = digest.Write(framed[:])
	_, _ = digest.Write(document)
	return makemigrationsTempPrefix + hex.EncodeToString(digest.Sum(nil))
}

func isLowerHex(value string) bool {
	for index := range value {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return value != ""
}

func readMakemigrationsRegularAt(directory *os.File, name string, maximum int) ([]byte, unix.Stat_t, bool, error) {
	var initial unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), name, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, unix.Stat_t{}, false, nil
		}
		return nil, unix.Stat_t{}, false, err
	}
	if initial.Mode&unix.S_IFMT != unix.S_IFREG || initial.Size < 0 || initial.Size > int64(maximum) {
		return nil, unix.Stat_t{}, false, errors.New("makemigrations member is not a bounded regular file")
	}
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, unix.Stat_t{}, false, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, unix.Stat_t{}, false, errors.New("retain makemigrations member")
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || opened.Mode&unix.S_IFMT != unix.S_IFREG ||
		!sameIdentity(initial, opened) || initial.Mode != opened.Mode || initial.Size != opened.Size {
		_ = file.Close()
		return nil, unix.Stat_t{}, false, errors.New("makemigrations member identity changed")
	}
	document, readErr := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if readErr == nil {
		if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
			readErr = seekErr
		} else {
			second, secondErr := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
			if secondErr != nil {
				readErr = secondErr
			} else if !bytes.Equal(document, second) {
				readErr = errors.New("makemigrations member changed while reading")
			}
			clear(second)
		}
	}
	var retainedAfter unix.Stat_t
	retainedErr := unix.Fstat(fd, &retainedAfter)
	closeErr := file.Close()
	var after unix.Stat_t
	postErr := unix.Fstatat(int(directory.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW)
	if readErr != nil || retainedErr != nil || closeErr != nil || len(document) > maximum || postErr != nil ||
		retainedAfter.Mode&unix.S_IFMT != unix.S_IFREG || after.Mode&unix.S_IFMT != unix.S_IFREG ||
		!sameIdentity(opened, retainedAfter) || !sameIdentity(retainedAfter, after) ||
		opened.Mode != retainedAfter.Mode || retainedAfter.Mode != after.Mode ||
		opened.Size != retainedAfter.Size || retainedAfter.Size != after.Size || after.Size != int64(len(document)) {
		clear(document)
		return nil, unix.Stat_t{}, false, errors.New("makemigrations member changed while reading")
	}
	return document, after, true, nil
}

func verifyMakemigrationsOwnedTempBeforeCleanup(
	root *makemigrationsWriterRoot,
	temp string,
	target string,
	expected unix.Stat_t,
	document []byte,
	requireExact bool,
) bool {
	if root == nil || !root.verify(true) || temp != makemigrationsTempBasename(target, document) {
		return false
	}
	if !requireExact {
		var current unix.Stat_t
		return unix.Fstatat(int(root.directory.Fd()), temp, &current, unix.AT_SYMLINK_NOFOLLOW) == nil &&
			current.Mode&unix.S_IFMT == unix.S_IFREG && uint64(current.Dev) == uint64(root.identity.Dev) &&
			sameIdentity(current, expected) && root.verify(true)
	}
	actual, current, present, err := readMakemigrationsRegularAt(root.directory, temp, definition.MaxDocumentBytes)
	matches := err == nil && present && current.Mode&0o7777 == 0o600 &&
		uint64(current.Dev) == uint64(root.identity.Dev) && sameIdentity(current, expected) &&
		current.Size == int64(len(document)) && bytes.Equal(actual, document) && root.verify(true)
	clear(actual)
	return matches
}

func makemigrationsTargetAbsent(directory *os.File, target string) bool {
	var stat unix.Stat_t
	err := unix.Fstatat(int(directory.Fd()), target, &stat, unix.AT_SYMLINK_NOFOLLOW)
	return errors.Is(err, unix.ENOENT)
}

func makemigrationsReservedTempRosterIsExactly(directory *os.File, expected string) bool {
	reader, err := duplicateMakemigrationsDirectory(directory)
	if err != nil {
		return false
	}
	defer reader.Close()
	found := false
	seen := 0
	for {
		entries, readErr := reader.ReadDir(makemigrationsDirectoryChunk)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return false
		}
		for _, entry := range entries {
			if seen >= makemigrationsMaxDirectoryEntries {
				return false
			}
			seen++
			if !strings.HasPrefix(entry.Name(), makemigrationsTempPrefix) {
				continue
			}
			if entry.Name() != expected || found {
				return false
			}
			found = true
		}
		if errors.Is(readErr, io.EOF) {
			return found == (expected != "")
		}
	}
}

func countMakemigrationsWriterDirectoryEntries(directory *os.File) (int, error) {
	reader, err := duplicateMakemigrationsDirectory(directory)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	seen := 0
	for {
		entries, readErr := reader.ReadDir(makemigrationsDirectoryChunk)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return 0, readErr
		}
		if len(entries) > makemigrationsMaxDirectoryEntries-seen {
			return makemigrationsMaxDirectoryEntries + 1, nil
		}
		seen += len(entries)
		if errors.Is(readErr, io.EOF) {
			return seen, nil
		}
	}
}

func makemigrationsDirectoryHasHeadroom(existing, candidates int) bool {
	return existing >= 0 && candidates >= 0 &&
		existing <= makemigrationsMaxDirectoryEntries &&
		candidates <= makemigrationsMaxDirectoryEntries-existing
}

func makemigrationsEntryIdentityMatches(directory *os.File, name string, expected unix.Stat_t) bool {
	var current unix.Stat_t
	return unix.Fstatat(int(directory.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil &&
		current.Mode&unix.S_IFMT == unix.S_IFREG && sameIdentity(current, expected)
}

func writeMakemigrationsAll(file *os.File, document []byte) error {
	for len(document) != 0 {
		written, err := file.Write(document)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(document) {
			return io.ErrShortWrite
		}
		document = document[written:]
	}
	return nil
}

func verifyMakemigrationsOpenTemp(file *os.File, expected unix.Stat_t, document []byte) (unix.Stat_t, error) {
	var current unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &current); err != nil || current.Mode&unix.S_IFMT != unix.S_IFREG ||
		current.Mode&0o7777 != 0o600 || !sameIdentity(current, expected) || current.Size != int64(len(document)) {
		return unix.Stat_t{}, errors.New("makemigrations temp changed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return unix.Stat_t{}, err
	}
	actual, err := io.ReadAll(io.LimitReader(file, int64(len(document))+1))
	if err != nil || !bytes.Equal(actual, document) {
		clear(actual)
		return unix.Stat_t{}, errors.New("makemigrations temp bytes changed")
	}
	clear(actual)
	return current, nil
}

func verifyMakemigrationsPublishedTarget(
	root *makemigrationsWriterRoot,
	target string,
	expected unix.Stat_t,
	document []byte,
) error {
	actual, stat, present, err := readMakemigrationsRegularAt(root.directory, target, definition.MaxDocumentBytes)
	defer clear(actual)
	if err != nil || !present || stat.Mode&0o7777 != 0o600 || !sameIdentity(stat, expected) || !bytes.Equal(actual, document) {
		return errors.New("makemigrations target verification failed")
	}
	return nil
}

func syncMakemigrationsWriterDirectory(
	hooks makemigrationsPublicationHooks,
	directory *os.File,
	step makemigrationsPublicationStep,
	index int,
	report *MakemigrationsReport,
) error {
	if err := hooks.syncDirectory(directory, step, index); err != nil {
		return err
	}
	report.PublicationDirectorySyncs++
	return nil
}
