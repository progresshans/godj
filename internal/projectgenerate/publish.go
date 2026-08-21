package projectgenerate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/progresshans/godj/codegen"
)

type publicationStep string

const (
	publicationStepLocked           publicationStep = "locked"
	publicationStepJournalDurable   publicationStep = "journal_durable"
	publicationStepStageFileDurable publicationStep = "stage_file_durable"
	publicationStepCandidateValid   publicationStep = "candidate_valid"
	publicationStepPriorCASValid    publicationStep = "prior_cas_valid"
	publicationStepDirectoryMade    publicationStep = "directory_created"
	publicationStepPriorBackedUp    publicationStep = "prior_backed_up"
	publicationStepNextInstalled    publicationStep = "next_installed"
	publicationStepManifestBackedUp publicationStep = "manifest_backed_up"
	publicationStepManifestRenamed  publicationStep = "manifest_renamed"
	publicationStepManifestCommit   publicationStep = "manifest_commit"
	publicationStepDirectoryCleaned publicationStep = "directory_cleaned"
	publicationStepTransactionEntry publicationStep = "transaction_entry_cleaned"
	publicationStepTransactionClean publicationStep = "transaction_cleaned"
	publicationStepJournalRemoved   publicationStep = "journal_removed"
	publicationStepCleanupComplete  publicationStep = "cleanup_complete"
)

type publicationHooks struct {
	after func(publicationStep, string, int) error
}

func (hooks publicationHooks) fire(step publicationStep, relative string, index int) error {
	if hooks.after == nil {
		return nil
	}
	return hooks.after(step, relative, index)
}

type publicationBundle struct {
	snapshot      string
	manifest      []byte
	manifestSHA   string
	files         []publicationFile
	filesByPath   map[string]publicationFile
	manifestModel committedManifest
}

type publicationFile struct {
	path   string
	owner  string
	sha256 string
	mode   uint32
	source []byte
}

// Publish verifies and publishes one immutable generated project bundle. A
// cancellation or ordinary failure before the manifest commit point restores
// the exact prior bundle. Once the manifest is durably committed, cleanup is
// completed without observing caller cancellation and the new bundle wins.
func Publish(ctx context.Context, projectRoot string, bundle codegen.GeneratedBundle, verifier CandidateVerifier) error {
	return publishWithHooks(ctx, projectRoot, bundle, verifier, publicationHooks{})
}

// PublishRoot publishes to the exact physical project identity sealed by the
// project selector.
func PublishRoot(ctx context.Context, projectRoot ProjectRoot, bundle codegen.GeneratedBundle, verifier CandidateVerifier) error {
	return publishRootWithHooks(ctx, projectRoot, bundle, verifier, publicationHooks{})
}

func publishWithHooks(
	ctx context.Context,
	projectRoot string,
	bundleValue codegen.GeneratedBundle,
	verifier CandidateVerifier,
	hooks publicationHooks,
) error {
	return publishRootWithHooks(ctx, ProjectRoot{absolute: projectRoot}, bundleValue, verifier, hooks)
}

func publishRootWithHooks(
	ctx context.Context,
	projectRoot ProjectRoot,
	bundleValue codegen.GeneratedBundle,
	verifier CandidateVerifier,
	hooks publicationHooks,
) error {
	if ctx == nil {
		return fmt.Errorf("publish generated project: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bundle, err := snapshotPublicationBundle(bundleValue)
	if err != nil {
		return err
	}
	if verifier == nil {
		return fmt.Errorf("%w: verifier is nil", ErrCandidateVerification)
	}
	root, err := openPublicationRootAuthority(projectRoot)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGeneratedConflict, err)
	}
	defer root.close()
	lock, err := acquirePublicationLock(ctx, root)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: %v", ErrGeneratedConflict, err)
	}
	defer lock.release()
	if err := hooks.fire(publicationStepLocked, publicationLockRelativePath, -1); err != nil {
		return err
	}
	if !root.verify() {
		return fmt.Errorf("%w: publication lock or project root changed", ErrGeneratedConflict)
	}
	if err := recoverExistingPublication(root); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	transactionID, err := newPublicationTransactionID()
	if err != nil {
		return fmt.Errorf("publish generated project: create transaction id: %w", err)
	}
	journal, unchanged, err := capturePublicationJournal(root, transactionID, bundle)
	if err != nil {
		return err
	}
	allowedGeneratedPaths := make(map[string]struct{}, len(journal.Files))
	for _, entry := range journal.Files {
		allowedGeneratedPaths[entry.Path] = struct{}{}
	}
	if err := verifyReservedGeneratedPreflight(ctx, root, allowedGeneratedPaths); err != nil {
		return err
	}
	if unchanged {
		parents, err := retainPublicationParents(root, journal)
		if err != nil {
			return fmt.Errorf("%w: retain unchanged target directories: %v", ErrGeneratedConflict, err)
		}
		defer parents.close()
		// The namespace scan deliberately skips the contents of allowed files.
		// Repeat the exact manifest/directory/file CAS after that potentially long
		// walk so an idempotent call cannot return stale success.
		return verifyPublicationPriorCAS(root, parents, journal)
	}
	parents, err := retainPublicationParents(root, journal)
	if err != nil {
		return fmt.Errorf("%w: retain target directories: %v", ErrGeneratedConflict, err)
	}
	defer parents.close()
	if err := verifyPublicationPriorCAS(root, parents, journal); err != nil {
		return err
	}
	tx, err := root.createTransaction(transactionID)
	if err != nil {
		if recoveryErr := recoverExistingPublication(root); recoveryErr != nil {
			return publicationRecoveryError(err, recoveryErr)
		}
		return fmt.Errorf("publish generated project: %w", err)
	}
	journalDocument, err := encodePublicationJournal(journal)
	if err != nil {
		_ = root.removeTransaction(tx, nil)
		return fmt.Errorf("publish generated project: %w", err)
	}
	if err := root.writeJournal(tx, journalDocument); err != nil {
		_ = tx.close()
		if recoveryErr := recoverExistingPublication(root); recoveryErr != nil {
			return publicationRecoveryError(err, recoveryErr)
		}
		return fmt.Errorf("publish generated project: persist journal: %w", err)
	}
	if err := hooks.fire(publicationStepJournalDurable, publicationJournalRelativePath, -1); err != nil {
		_ = tx.close()
		if recoveryErr := recoverExistingPublication(root); recoveryErr != nil {
			return publicationRecoveryError(err, recoveryErr)
		}
		return err
	}

	failBeforeCommit := func(primary error) error {
		_ = tx.close()
		if recoveryErr := recoverExistingPublication(root); recoveryErr != nil {
			return publicationRecoveryError(primary, recoveryErr)
		}
		// A concurrent actor can install the exact next manifest after the prior
		// marker was backed up. Recovery deliberately accepts that marker only
		// when every next file/directory is exact and makes it durable. In that
		// case the operation has committed despite the earlier ordinary error, so
		// do not report a failed/rolled-back publication.
		if nextErr := verifyCompleteNextState(root, journal); nextErr == nil {
			return nil
		}
		return primary
	}
	for index, file := range bundle.files {
		if err := ctx.Err(); err != nil {
			return failBeforeCommit(err)
		}
		if err := tx.writeStageFile(file.path, file.source, file.mode, root.device); err != nil {
			return failBeforeCommit(fmt.Errorf("publish generated project: stage %s: %w", file.path, err))
		}
		if err := hooks.fire(publicationStepStageFileDurable, file.path, index); err != nil {
			return failBeforeCommit(err)
		}
	}
	if err := tx.writeStageFile(generatedManifestRelativePath, bundle.manifest, 0o644, root.device); err != nil {
		return failBeforeCommit(fmt.Errorf("publish generated project: stage manifest: %w", err))
	}
	if err := hooks.fire(publicationStepStageFileDurable, generatedManifestRelativePath, len(bundle.files)); err != nil {
		return failBeforeCommit(err)
	}
	stageRoot, err := tx.stageRoot(root)
	if err != nil {
		return failBeforeCommit(fmt.Errorf("%w: %v", ErrGeneratedConflict, err))
	}
	if !root.verify() {
		return failBeforeCommit(fmt.Errorf("%w: project root changed", ErrGeneratedConflict))
	}
	if err := verifier.Verify(ctx, stageRoot); err != nil {
		return failBeforeCommit(fmt.Errorf("%w: %w", ErrCandidateVerification, err))
	}
	if _, err := tx.stageRoot(root); err != nil {
		return failBeforeCommit(fmt.Errorf("%w: %v", ErrGeneratedConflict, err))
	}
	if err := verifyStagedPublication(tx, bundle, root.device); err != nil {
		return failBeforeCommit(fmt.Errorf("%w: staged candidate changed after verification: %v", ErrGeneratedConflict, err))
	}
	if err := hooks.fire(publicationStepCandidateValid, "", -1); err != nil {
		return failBeforeCommit(err)
	}
	if err := ctx.Err(); err != nil {
		return failBeforeCommit(err)
	}
	if err := verifyReservedGeneratedPreflight(ctx, root, allowedGeneratedPaths); err != nil {
		return failBeforeCommit(err)
	}
	if err := verifyPublicationPriorCAS(root, parents, journal); err != nil {
		return failBeforeCommit(err)
	}
	if err := verifyStagedPublication(tx, bundle, root.device); err != nil {
		return failBeforeCommit(fmt.Errorf("%w: staged candidate changed before publication: %v", ErrGeneratedConflict, err))
	}
	if err := hooks.fire(publicationStepPriorCASValid, "", -1); err != nil {
		return failBeforeCommit(err)
	}
	for index, directory := range journal.Directories {
		if directory.PriorPresent {
			continue
		}
		if err := ctx.Err(); err != nil {
			return failBeforeCommit(err)
		}
		if err := root.createTargetDirectory(directory.Path, tx, index, parents); err != nil {
			return failBeforeCommit(fmt.Errorf("publish generated project: create directory %s: %w", directory.Path, err))
		}
		if err := hooks.fire(publicationStepDirectoryMade, directory.Path, index); err != nil {
			return failBeforeCommit(err)
		}
	}

	for index, entry := range journal.Files {
		if err := ctx.Err(); err != nil {
			return failBeforeCommit(err)
		}
		if fileStatesEqual(entry.Prior, entry.Next) {
			continue
		}
		if entry.Prior.Present {
			if !entry.Prior.Owned {
				return failBeforeCommit(fmt.Errorf("%w: refuse to replace unowned %s", ErrGeneratedConflict, entry.Path))
			}
			if err := verifyObservedStateRetained(root, parents, entry.Path, entry.Prior); err != nil {
				return failBeforeCommit(err)
			}
			if err := root.renameTargetToBackupRetained(entry.Path, tx, index, parents); err != nil {
				return failBeforeCommit(fmt.Errorf("publish generated project: backup %s: %w", entry.Path, err))
			}
			if err := verifyBackupState(tx, index, entry.Prior); err != nil {
				return failBeforeCommit(err)
			}
			if err := hooks.fire(publicationStepPriorBackedUp, entry.Path, index); err != nil {
				return failBeforeCommit(err)
			}
		}
		if entry.Next.Present {
			if err := verifyStagedFile(tx, entry.Path, entry.Next, root.device); err != nil {
				return failBeforeCommit(fmt.Errorf("%w: staged file %s changed: %v", ErrGeneratedConflict, entry.Path, err))
			}
			if err := verifyObservedStateRetained(root, parents, entry.Path, journalFileState{}); err != nil {
				return failBeforeCommit(err)
			}
			if err := root.renameStageToTargetRetained(entry.Path, tx, parents); err != nil {
				return failBeforeCommit(fmt.Errorf("publish generated project: install %s: %w", entry.Path, err))
			}
			if err := verifyObservedStateRetained(root, parents, entry.Path, entry.Next); err != nil {
				return failBeforeCommit(err)
			}
			if err := hooks.fire(publicationStepNextInstalled, entry.Path, index); err != nil {
				return failBeforeCommit(err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return failBeforeCommit(err)
	}

	manifestIndex := len(journal.Files)
	if journal.PriorManifest.Present {
		if err := verifyManifestState(root, journal.PriorManifest); err != nil {
			return failBeforeCommit(err)
		}
		if err := root.renameManifestToBackup(tx, manifestIndex); err != nil {
			return failBeforeCommit(fmt.Errorf("publish generated project: backup manifest: %w", err))
		}
		if err := verifyBackupManifest(tx, manifestIndex, journal.PriorManifest); err != nil {
			return failBeforeCommit(err)
		}
		if err := hooks.fire(publicationStepManifestBackedUp, generatedManifestRelativePath, manifestIndex); err != nil {
			return failBeforeCommit(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return failBeforeCommit(err)
	}
	if err := verifyTargetAbsent(root, generatedManifestRelativePath); err != nil {
		return failBeforeCommit(err)
	}
	if err := ctx.Err(); err != nil {
		return failBeforeCommit(err)
	}
	if err := verifyStagedManifest(tx, journal.NextManifest, root.device); err != nil {
		return failBeforeCommit(fmt.Errorf("%w: staged manifest changed: %v", ErrGeneratedConflict, err))
	}
	if err := ctx.Err(); err != nil {
		return failBeforeCommit(err)
	}
	manifestStageParent, err := root.renameStageManifestToTarget(tx)
	if err != nil {
		return failBeforeCommit(fmt.Errorf("publish generated project: commit manifest: %w", err))
	}
	if err := hooks.fire(publicationStepManifestRenamed, generatedManifestRelativePath, manifestIndex); err != nil {
		_ = manifestStageParent.Close()
		_ = tx.close()
		return fmt.Errorf("%w: commit marker was renamed before durability proof: %v", ErrPublicationRecoveryRequired, err)
	}
	if err := verifyManifestState(root, journal.NextManifest); err != nil {
		_ = manifestStageParent.Close()
		_ = tx.close()
		return fmt.Errorf("%w: commit marker rename is not provably durable: %v", ErrPublicationRecoveryRequired, err)
	}
	if err := syncDirectory(root.godj); err != nil {
		_ = manifestStageParent.Close()
		_ = tx.close()
		return fmt.Errorf("%w: sync renamed commit marker: %v", ErrPublicationRecoveryRequired, err)
	}

	// The destination directory fsync above is the in-process commit point.
	// The source stage directory is transaction cleanup state: once committed,
	// failure to sync or close it must not be reported as a rolled-back publish.
	if sourceErr := errors.Join(syncDirectory(manifestStageParent), manifestStageParent.Close()); sourceErr != nil {
		_ = tx.close()
		if recoveryErr := recoverExistingPublication(root); recoveryErr != nil {
			return publicationRecoveryError(sourceErr, recoveryErr)
		}
		return nil
	}

	// The manifest is the durable commit marker. From here caller cancellation
	// cannot turn the committed new bundle into an apparent failed operation.
	if err := hooks.fire(publicationStepManifestCommit, generatedManifestRelativePath, manifestIndex); err != nil {
		_ = tx.close()
		if recoveryErr := recoverExistingPublication(root); recoveryErr != nil {
			return publicationRecoveryError(err, recoveryErr)
		}
		return nil
	}
	_ = tx.close()
	if err := recoverExistingPublicationWithHooks(root, hooks); err != nil {
		return err
	}
	if err := hooks.fire(publicationStepCleanupComplete, "", -1); err != nil {
		return nil
	}
	return nil
}

func verifyReservedGeneratedPreflight(
	ctx context.Context,
	root *publicationRoot,
	allowed map[string]struct{},
) error {
	unownedGenerated, namespaceConflict, scanErr := scanReservedGeneratedNamespace(ctx, root.absolute, allowed)
	if scanErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: scan reserved generated namespace: %v", ErrGeneratedConflict, scanErr)
	}
	if !root.verify() {
		return fmt.Errorf("%w: project root changed while scanning reserved generated namespace", ErrGeneratedConflict)
	}
	if namespaceConflict || len(unownedGenerated) != 0 {
		return fmt.Errorf("%w: reserved generated namespace contains %d unowned entries", ErrGeneratedConflict, len(unownedGenerated))
	}
	return nil
}

func snapshotPublicationBundle(bundle codegen.GeneratedBundle) (publicationBundle, error) {
	snapshot := bundle.SnapshotSHA256()
	manifestDocument := bundle.Manifest()
	files := bundle.Files()
	if !validSHA256(snapshot) || len(manifestDocument) == 0 || len(files) > maximumPublicationJournalFiles {
		return publicationBundle{}, fmt.Errorf("%w: invalid bundle envelope", ErrInvalidGeneratedBundle)
	}
	manifest, err := validateGeneratedBundle(bundle)
	if err != nil {
		return publicationBundle{}, err
	}
	if manifest.SnapshotSHA256 != snapshot || len(manifest.Files) != len(files) {
		return publicationBundle{}, fmt.Errorf("%w: manifest inventory differs from bundle", ErrInvalidGeneratedBundle)
	}
	result := publicationBundle{
		snapshot:      snapshot,
		manifest:      append([]byte(nil), manifestDocument...),
		manifestSHA:   digestBytes(manifestDocument),
		files:         make([]publicationFile, len(files)),
		filesByPath:   make(map[string]publicationFile, len(files)),
		manifestModel: manifest,
	}
	previous := ""
	for index, file := range files {
		if err := validatePublicationRelativePath(file.Path); err != nil || file.Path == generatedManifestRelativePath || file.Path == ".godj" || strings.HasPrefix(file.Path, ".godj/") {
			return publicationBundle{}, fmt.Errorf("%w: files[%d] has invalid path", ErrInvalidGeneratedBundle, index)
		}
		if file.Path <= previous && index != 0 {
			return publicationBundle{}, fmt.Errorf("%w: files are not strictly ordered", ErrInvalidGeneratedBundle)
		}
		previous = file.Path
		if file.Mode != fs.FileMode(0o644) || !validSHA256(file.SHA256) {
			return publicationBundle{}, fmt.Errorf("%w: files[%d] has invalid mode or digest", ErrInvalidGeneratedBundle, index)
		}
		source := file.Source()
		if len(source) > maximumPublicationFileBytes || digestBytes(source) != file.SHA256 {
			return publicationBundle{}, fmt.Errorf("%w: files[%d] source digest differs", ErrInvalidGeneratedBundle, index)
		}
		manifestFile := manifest.Files[index]
		mode, modeErr := parseManifestMode(manifestFile.Mode)
		if modeErr != nil || manifestFile.Path != file.Path || manifestFile.Owner != file.Owner || manifestFile.SHA256 != file.SHA256 || mode != uint32(file.Mode) {
			return publicationBundle{}, fmt.Errorf("%w: files[%d] manifest member differs", ErrInvalidGeneratedBundle, index)
		}
		entry := publicationFile{path: file.Path, owner: file.Owner, sha256: file.SHA256, mode: uint32(file.Mode), source: source}
		result.files[index] = entry
		result.filesByPath[entry.path] = entry
	}
	return result, nil
}

func capturePublicationJournal(root *publicationRoot, transactionID string, bundle publicationBundle) (publicationJournal, bool, error) {
	journal := publicationJournal{
		FormatVersion:  publicationJournalFormatVersion,
		TransactionID:  transactionID,
		SnapshotSHA256: bundle.snapshot,
		NextManifest:   journalManifestState{Present: true, SHA256: bundle.manifestSHA},
	}
	priorFiles := make(map[string]journalFileState)
	directoryPaths, err := publicationBundleDirectories(bundle.files)
	if err != nil {
		return publicationJournal{}, false, fmt.Errorf("%w: %v", ErrInvalidGeneratedBundle, err)
	}
	journal.Directories = make([]journalDirectory, len(directoryPaths))
	for index, relative := range directoryPaths {
		present, observeErr := root.observeDirectory(relative)
		if observeErr != nil {
			return publicationJournal{}, false, fmt.Errorf("%w: inspect target directory %s: %v", ErrGeneratedConflict, relative, observeErr)
		}
		journal.Directories[index] = journalDirectory{Path: relative, PriorPresent: present}
	}
	manifestObserved, manifestBytes, err := root.observe(generatedManifestRelativePath, maximumPublicationJournalBytes)
	if err != nil {
		return publicationJournal{}, false, fmt.Errorf("%w: read prior manifest: %v", ErrGeneratedConflict, err)
	}
	if manifestObserved.present {
		if manifestObserved.mode != 0o644 {
			return publicationJournal{}, false, fmt.Errorf("%w: prior manifest mode differs", ErrGeneratedConflict)
		}
		priorManifest, decodeErr := decodeCommittedManifest(manifestBytes)
		if decodeErr != nil {
			return publicationJournal{}, false, fmt.Errorf("%w: prior manifest is invalid", ErrGeneratedConflict)
		}
		journal.PriorManifest = journalManifestState{Present: true, SHA256: manifestObserved.sha256}
		for index, file := range priorManifest.Files {
			if err := validatePublicationRelativePath(file.Path); err != nil || file.Path == generatedManifestRelativePath || file.Path == ".godj" || strings.HasPrefix(file.Path, ".godj/") {
				return publicationJournal{}, false, fmt.Errorf("%w: prior manifest files[%d] path is unsafe", ErrGeneratedConflict, index)
			}
			mode, modeErr := parseManifestMode(file.Mode)
			if modeErr != nil || !validSHA256(file.SHA256) {
				return publicationJournal{}, false, fmt.Errorf("%w: prior manifest files[%d] is invalid", ErrGeneratedConflict, index)
			}
			observed, _, observeErr := root.observe(file.Path, maximumPublicationFileBytes)
			if observeErr != nil || !observed.present || observed.sha256 != file.SHA256 || observed.mode != mode {
				return publicationJournal{}, false, fmt.Errorf("%w: prior-owned file %s changed", ErrGeneratedConflict, file.Path)
			}
			priorFiles[file.Path] = journalFileState{Present: true, Owned: true, SHA256: file.SHA256, Mode: mode}
		}
	}

	paths := make([]string, 0, len(priorFiles)+len(bundle.files))
	seen := make(map[string]struct{}, len(priorFiles)+len(bundle.files))
	for relative := range priorFiles {
		seen[relative] = struct{}{}
		paths = append(paths, relative)
	}
	for _, file := range bundle.files {
		if _, exists := seen[file.path]; !exists {
			paths = append(paths, file.path)
		}
	}
	sort.Strings(paths)
	journal.Files = make([]journalFile, 0, len(paths))
	for _, relative := range paths {
		prior, priorOwned := priorFiles[relative]
		nextFile, nextExists := bundle.filesByPath[relative]
		if !priorOwned {
			observed, _, observeErr := root.observe(relative, maximumPublicationFileBytes)
			if observeErr != nil {
				return publicationJournal{}, false, fmt.Errorf("%w: inspect unowned target %s: %v", ErrGeneratedConflict, relative, observeErr)
			}
			if observed.present {
				if !nextExists || observed.sha256 != nextFile.sha256 || observed.mode != nextFile.mode {
					return publicationJournal{}, false, fmt.Errorf("%w: unowned target %s differs", ErrGeneratedConflict, relative)
				}
				prior = journalFileState{Present: true, Owned: false, SHA256: observed.sha256, Mode: observed.mode}
			}
		}
		next := journalFileState{}
		if nextExists {
			next = journalFileState{Present: true, Owned: true, SHA256: nextFile.sha256, Mode: nextFile.mode}
		}
		journal.Files = append(journal.Files, journalFile{Path: relative, Prior: prior, Next: next})
	}
	if err := validatePublicationJournal(journal); err != nil {
		return publicationJournal{}, false, fmt.Errorf("%w: %v", ErrInvalidGeneratedBundle, err)
	}
	unchanged := journal.PriorManifest.Present && journal.PriorManifest.SHA256 == journal.NextManifest.SHA256
	if unchanged {
		for _, file := range journal.Files {
			if !fileStatesEqual(file.Prior, file.Next) {
				unchanged = false
				break
			}
		}
	}
	return journal, unchanged, nil
}

func verifyPublicationPriorCAS(root *publicationRoot, parents *retainedPublicationParents, journal publicationJournal) error {
	if !root.verify() {
		return fmt.Errorf("%w: project root changed", ErrGeneratedConflict)
	}
	if err := verifyManifestState(root, journal.PriorManifest); err != nil {
		return err
	}
	for _, directory := range journal.Directories {
		present, err := root.observeDirectory(directory.Path)
		if err != nil || present != directory.PriorPresent {
			return fmt.Errorf("%w: directory %s changed during publication", ErrGeneratedConflict, directory.Path)
		}
	}
	for _, entry := range journal.Files {
		if err := verifyObservedStateRetained(root, parents, entry.Path, entry.Prior); err != nil {
			return err
		}
	}
	return nil
}

func verifyObservedStateRetained(root *publicationRoot, parents *retainedPublicationParents, relative string, expected journalFileState) error {
	if parents == nil || parents.byRelative[path.Dir(relative)] == nil {
		if expected.Present {
			return fmt.Errorf("%w: target parent for %s is missing", ErrGeneratedConflict, relative)
		}
		observed, _, err := root.observe(relative, maximumPublicationFileBytes)
		if err != nil || observed.present {
			return fmt.Errorf("%w: absent target %s changed: %v", ErrGeneratedConflict, relative, err)
		}
		return nil
	}
	parent, name, err := parents.forTarget(root, relative)
	if err != nil {
		return fmt.Errorf("%w: target parent for %s changed: %v", ErrGeneratedConflict, relative, err)
	}
	observed, _, err := observeRegularAt(parent.directory, name, maximumPublicationFileBytes)
	if err != nil {
		return fmt.Errorf("%w: inspect %s: %v", ErrGeneratedConflict, relative, err)
	}
	if observed.present != expected.Present || (expected.Present && (observed.sha256 != expected.SHA256 || observed.mode != expected.Mode)) {
		return fmt.Errorf("%w: %s changed during publication", ErrGeneratedConflict, relative)
	}
	return nil
}

func publicationBundleDirectories(files []publicationFile) ([]string, error) {
	seen := make(map[string]struct{})
	for _, file := range files {
		current := path.Dir(file.path)
		for current != "." {
			seen[current] = struct{}{}
			if len(seen) > maximumPublicationJournalFiles {
				return nil, fmt.Errorf("generated directory inventory exceeds limit")
			}
			current = path.Dir(current)
		}
	}
	directories := make([]string, 0, len(seen))
	for directory := range seen {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(left, right int) bool {
		return publicationDirectoryLess(directories[left], directories[right])
	})
	return directories, nil
}

func verifyObservedState(root *publicationRoot, relative string, expected journalFileState) error {
	observed, _, err := root.observe(relative, maximumPublicationFileBytes)
	if err != nil {
		return fmt.Errorf("%w: inspect %s: %v", ErrGeneratedConflict, relative, err)
	}
	if observed.present != expected.Present || (expected.Present && (observed.sha256 != expected.SHA256 || observed.mode != expected.Mode)) {
		return fmt.Errorf("%w: %s changed during publication", ErrGeneratedConflict, relative)
	}
	return nil
}

func verifyTargetAbsent(root *publicationRoot, relative string) error {
	return verifyObservedState(root, relative, journalFileState{})
}

func verifyManifestState(root *publicationRoot, expected journalManifestState) error {
	observed, _, err := root.observe(generatedManifestRelativePath, maximumPublicationJournalBytes)
	if err != nil {
		return fmt.Errorf("%w: inspect manifest: %v", ErrGeneratedConflict, err)
	}
	if observed.present != expected.Present || (expected.Present && (observed.sha256 != expected.SHA256 || observed.mode != 0o644)) {
		return fmt.Errorf("%w: manifest changed during publication", ErrGeneratedConflict)
	}
	return nil
}

func verifyBackupState(tx *publicationTransaction, index int, expected journalFileState) error {
	observed, _, err := observeRegularAt(tx.backup, publicationBackupName(index), maximumPublicationFileBytes)
	if err != nil || !observed.present || observed.sha256 != expected.SHA256 || observed.mode != expected.Mode {
		return fmt.Errorf("%w: backup state is ambiguous", ErrPublicationRecoveryRequired)
	}
	return nil
}

func verifyBackupManifest(tx *publicationTransaction, index int, expected journalManifestState) error {
	observed, _, err := observeRegularAt(tx.backup, publicationBackupName(index), maximumPublicationJournalBytes)
	if err != nil || !observed.present || observed.sha256 != expected.SHA256 || observed.mode != 0o644 {
		return fmt.Errorf("%w: manifest backup is ambiguous", ErrPublicationRecoveryRequired)
	}
	return nil
}

func fileStatesEqual(left, right journalFileState) bool {
	return left.Present == right.Present && left.SHA256 == right.SHA256 && left.Mode == right.Mode
}

func parseManifestMode(value string) (uint32, error) {
	if len(value) != 4 || value[0] != '0' {
		return 0, fmt.Errorf("invalid generated mode")
	}
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil || parsed != 0o644 {
		return 0, fmt.Errorf("invalid generated mode")
	}
	return uint32(parsed), nil
}

func validatePublicationRelativePath(value string) error {
	if value == "" || len(value) > maximumPublicationPathBytes || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return fmt.Errorf("invalid publication path")
	}
	if path.Clean(value) != value || value == "." || value == ".." {
		return fmt.Errorf("noncanonical publication path")
	}
	components := strings.Split(value, "/")
	if len(components) > maximumPublicationPathDepth {
		return fmt.Errorf("publication path depth exceeds limit")
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("unsafe publication path")
		}
	}
	return nil
}

func newPublicationTransactionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func publicationRecoveryError(primary, recovery error) error {
	return fmt.Errorf("%w: %v; recovery: %v", ErrPublicationRecoveryRequired, primary, recovery)
}
