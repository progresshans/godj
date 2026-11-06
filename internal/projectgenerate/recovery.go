package projectgenerate

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

func recoverExistingPublication(root *publicationRoot) error {
	return recoverExistingPublicationWithHooks(root, publicationHooks{})
}

func recoverExistingPublicationWithHooks(root *publicationRoot, hooks publicationHooks) error {
	journalObserved, journalBytes, err := root.observe(publicationJournalRelativePath, maximumPublicationJournalBytes)
	if err != nil {
		return fmt.Errorf("%w: read publication journal: %v", ErrPublicationRecoveryRequired, err)
	}
	transactionNames, err := root.transactionNames()
	if err != nil {
		return fmt.Errorf("%w: enumerate publication transactions: %v", ErrPublicationRecoveryRequired, err)
	}
	sort.Strings(transactionNames)
	if !journalObserved.present {
		for _, transactionID := range transactionNames {
			if err := root.removeOrphanTransaction(transactionID); err != nil {
				return fmt.Errorf("%w: remove orphan transaction: %v", ErrPublicationRecoveryRequired, err)
			}
		}
		return nil
	}
	if journalObserved.mode != 0o600 {
		return fmt.Errorf("%w: publication journal mode differs", ErrPublicationRecoveryRequired)
	}
	journal, err := decodePublicationJournal(journalBytes)
	if err != nil {
		return fmt.Errorf("%w: invalid publication journal: %v", ErrPublicationRecoveryRequired, err)
	}
	for _, transactionID := range transactionNames {
		if transactionID != journal.TransactionID {
			return fmt.Errorf("%w: journal has unrelated transaction", ErrPublicationRecoveryRequired)
		}
	}

	tx, openErr := root.openTransaction(journal.TransactionID)
	if openErr != nil && !errors.Is(openErr, os.ErrNotExist) {
		return fmt.Errorf("%w: open publication transaction: %v", ErrPublicationRecoveryRequired, openErr)
	}
	if tx != nil {
		defer tx.close()
	}

	manifestObserved, manifestDocument, err := root.observe(generatedManifestRelativePath, maximumPublicationJournalBytes)
	if err != nil {
		return fmt.Errorf("%w: inspect commit marker: %v", ErrPublicationRecoveryRequired, err)
	}
	if manifestObserved.present && manifestObserved.mode != 0o644 {
		return fmt.Errorf("%w: commit marker mode differs", ErrPublicationRecoveryRequired)
	}
	if manifestObserved.present && manifestObserved.sha256 == journal.NextManifest.SHA256 {
		nextManifest, decodeErr := decodeRecoveryManifest(manifestDocument, journal.NextManifest.SHA256)
		if decodeErr != nil {
			return fmt.Errorf("%w: committed next manifest is invalid: %v", ErrPublicationRecoveryRequired, decodeErr)
		}
		if err := validateRecoveryJournalManifestBinding(journal, nil, &nextManifest, false); err != nil {
			return fmt.Errorf("%w: committed journal authority differs: %v", ErrPublicationRecoveryRequired, err)
		}
		if err := verifyCompleteNextState(root, journal); err != nil {
			return err
		}
		// A prior attempt may have returned RecoveryRequired immediately after
		// renaming the next manifest but before proving its destination directory
		// durable. Make the selected commit marker durable before deleting any
		// transaction or backup authority.
		if err := syncDirectory(root.godj); err != nil {
			return fmt.Errorf("%w: sync recovered commit marker: %v", ErrPublicationRecoveryRequired, err)
		}
		for index, directory := range journal.Directories {
			if !directory.PriorPresent {
				if err := root.cleanupCreatedDirectoryMarker(directory.Path, journal.TransactionID); err != nil {
					return fmt.Errorf("%w: clean committed directory %s: %v", ErrPublicationRecoveryRequired, directory.Path, err)
				}
				if err := hooks.fire(publicationStepDirectoryCleaned, directory.Path, index); err != nil {
					return fmt.Errorf("%w: committed directory cleanup interrupted: %v", ErrPublicationRecoveryRequired, err)
				}
			}
		}
		if tx != nil {
			if err := root.removeTransactionWithHooks(tx, &journal, hooks); err != nil {
				return fmt.Errorf("%w: clean committed transaction: %v", ErrPublicationRecoveryRequired, err)
			}
			tx = nil
			if err := hooks.fire(publicationStepTransactionClean, publicationTransactionDirectoryPath, -1); err != nil {
				return fmt.Errorf("%w: committed transaction cleanup interrupted: %v", ErrPublicationRecoveryRequired, err)
			}
		}
		if err := verifyCompleteNextState(root, journal); err != nil {
			return fmt.Errorf("%w: committed state changed during cleanup: %v", ErrPublicationRecoveryRequired, err)
		}
		if err := root.removeJournal(journalObserved); err != nil {
			return fmt.Errorf("%w: remove committed journal: %v", ErrPublicationRecoveryRequired, err)
		}
		if err := hooks.fire(publicationStepJournalRemoved, publicationJournalRelativePath, -1); err != nil {
			return fmt.Errorf("%w: committed journal cleanup interrupted: %v", ErrPublicationRecoveryRequired, err)
		}
		return nil
	}
	if manifestObserved.present && (!journal.PriorManifest.Present || manifestObserved.sha256 != journal.PriorManifest.SHA256) {
		return fmt.Errorf("%w: commit marker is neither prior nor next", ErrPublicationRecoveryRequired)
	}
	if !manifestObserved.present && !journal.PriorManifest.Present {
		// Exact pre-first-publication marker state.
	} else if !manifestObserved.present && journal.PriorManifest.Present {
		// The prior manifest may already have moved to its backup.
	} else if manifestObserved.sha256 != journal.PriorManifest.SHA256 {
		return fmt.Errorf("%w: prior manifest proof failed", ErrPublicationRecoveryRequired)
	}
	if priorErr := verifyCompletePriorState(root, journal); priorErr == nil {
		var priorManifest *committedManifest
		if journal.PriorManifest.Present {
			decoded, decodeErr := decodeRecoveryManifest(manifestDocument, journal.PriorManifest.SHA256)
			if decodeErr != nil {
				return fmt.Errorf("%w: exact prior manifest is invalid: %v", ErrPublicationRecoveryRequired, decodeErr)
			}
			priorManifest = &decoded
		}
		if err := validateRecoveryJournalManifestBinding(journal, priorManifest, nil, false); err != nil {
			return fmt.Errorf("%w: exact prior journal authority differs: %v", ErrPublicationRecoveryRequired, err)
		}
		// The visible prior state may be the result of a rollback that was
		// interrupted between rename and parent-directory fsync. Fence every
		// surviving target parent (including the project root and .godj) before
		// deleting backup/journal authority.
		if err := syncRecoveredProjectState(root, journal); err != nil {
			return fmt.Errorf("%w: sync recovered prior state: %v", ErrPublicationRecoveryRequired, err)
		}
		if tx != nil {
			if err := root.removeTransactionWithHooks(tx, &journal, hooks); err != nil {
				return fmt.Errorf("%w: clean exact-prior transaction: %v", ErrPublicationRecoveryRequired, err)
			}
			tx = nil
		} else if len(transactionNames) != 0 {
			return fmt.Errorf("%w: exact-prior transaction could not be opened", ErrPublicationRecoveryRequired)
		}
		if err := verifyCompletePriorState(root, journal); err != nil {
			return fmt.Errorf("%w: prior state changed during cleanup: %v", ErrPublicationRecoveryRequired, err)
		}
		if err := root.removeJournal(journalObserved); err != nil {
			return fmt.Errorf("%w: remove exact-prior journal: %v", ErrPublicationRecoveryRequired, err)
		}
		return nil
	}
	priorManifest, err := loadRecoveryPriorManifest(root, tx, journal, manifestObserved, manifestDocument)
	if err != nil {
		return err
	}
	nextManifest, err := loadRecoveryNextManifest(root, tx, journal, manifestObserved, manifestDocument)
	if err != nil {
		return err
	}
	if err := validateRecoveryJournalManifestBinding(journal, priorManifest, nextManifest, true); err != nil {
		return fmt.Errorf("%w: transitional journal authority differs: %v", ErrPublicationRecoveryRequired, err)
	}
	if err := rollbackPublication(root, tx, journal); err != nil {
		return err
	}
	if tx != nil {
		if err := root.removeTransactionWithHooks(tx, &journal, hooks); err != nil {
			return fmt.Errorf("%w: clean rolled-back transaction: %v", ErrPublicationRecoveryRequired, err)
		}
		tx = nil
	} else if len(transactionNames) != 0 {
		return fmt.Errorf("%w: transaction could not be opened", ErrPublicationRecoveryRequired)
	}
	if err := verifyCompletePriorState(root, journal); err != nil {
		return fmt.Errorf("%w: rolled-back state changed during cleanup: %v", ErrPublicationRecoveryRequired, err)
	}
	if err := root.removeJournal(journalObserved); err != nil {
		return fmt.Errorf("%w: remove rolled-back journal: %v", ErrPublicationRecoveryRequired, err)
	}
	return nil
}

func syncRecoveredProjectState(root *publicationRoot, journal publicationJournal) error {
	parents, err := retainPublicationParents(root, journal)
	if err != nil {
		return err
	}
	defer parents.close()
	if !root.verify() {
		return fmt.Errorf("project root changed")
	}
	if err := syncDirectory(root.godj); err != nil {
		return err
	}
	relatives := make([]string, 0, len(parents.byRelative))
	for relative := range parents.byRelative {
		relatives = append(relatives, relative)
	}
	sort.Strings(relatives)
	for _, relative := range relatives {
		parent := parents.byRelative[relative]
		if parent == nil || parent.directory == nil || !parent.bindingMatches(root) {
			return fmt.Errorf("target parent %s changed", relative)
		}
		if err := syncDirectory(parent.directory); err != nil {
			return err
		}
	}
	return nil
}

func decodeRecoveryManifest(document []byte, expectedSHA256 string) (committedManifest, error) {
	if digestBytes(document) != expectedSHA256 {
		return committedManifest{}, fmt.Errorf("manifest digest differs")
	}
	manifest, err := decodeCommittedManifest(document)
	if err != nil {
		return committedManifest{}, err
	}
	return manifest, nil
}

func loadRecoveryPriorManifest(
	root *publicationRoot,
	tx *publicationTransaction,
	journal publicationJournal,
	current observedFile,
	currentDocument []byte,
) (*committedManifest, error) {
	if !journal.PriorManifest.Present {
		return nil, nil
	}
	if current.present && current.sha256 == journal.PriorManifest.SHA256 {
		manifest, err := decodeRecoveryManifest(currentDocument, journal.PriorManifest.SHA256)
		if err != nil {
			return nil, fmt.Errorf("%w: prior manifest is invalid: %v", ErrPublicationRecoveryRequired, err)
		}
		return &manifest, nil
	}
	if tx == nil || tx.backup == nil {
		return nil, fmt.Errorf("%w: prior manifest authority is unavailable", ErrPublicationRecoveryRequired)
	}
	observed, document, err := observeRegularAt(tx.backup, publicationBackupName(len(journal.Files)), maximumPublicationJournalBytes)
	if err != nil || !observed.present || observed.sha256 != journal.PriorManifest.SHA256 || observed.mode != 0o644 {
		return nil, fmt.Errorf("%w: prior manifest backup is unavailable", ErrPublicationRecoveryRequired)
	}
	manifest, err := decodeRecoveryManifest(document, journal.PriorManifest.SHA256)
	if err != nil {
		return nil, fmt.Errorf("%w: prior manifest backup is invalid: %v", ErrPublicationRecoveryRequired, err)
	}
	return &manifest, nil
}

func loadRecoveryNextManifest(
	root *publicationRoot,
	tx *publicationTransaction,
	journal publicationJournal,
	current observedFile,
	currentDocument []byte,
) (*committedManifest, error) {
	if current.present && current.sha256 == journal.NextManifest.SHA256 {
		manifest, err := decodeRecoveryManifest(currentDocument, journal.NextManifest.SHA256)
		if err != nil {
			return nil, fmt.Errorf("%w: next manifest is invalid: %v", ErrPublicationRecoveryRequired, err)
		}
		return &manifest, nil
	}
	if tx == nil || tx.stage == nil {
		return nil, fmt.Errorf("%w: next manifest authority is unavailable", ErrPublicationRecoveryRequired)
	}
	parent, name, err := openParentAt(tx.stage, generatedManifestRelativePath, root.device)
	if err != nil {
		return nil, fmt.Errorf("%w: open staged next manifest: %v", ErrPublicationRecoveryRequired, err)
	}
	defer parent.Close()
	observed, document, err := observeRegularAt(parent, name, maximumPublicationJournalBytes)
	if err != nil || !observed.present || observed.sha256 != journal.NextManifest.SHA256 || observed.mode != 0o644 {
		return nil, fmt.Errorf("%w: staged next manifest is unavailable", ErrPublicationRecoveryRequired)
	}
	manifest, err := decodeRecoveryManifest(document, journal.NextManifest.SHA256)
	if err != nil {
		return nil, fmt.Errorf("%w: staged next manifest is invalid: %v", ErrPublicationRecoveryRequired, err)
	}
	return &manifest, nil
}

func validateRecoveryJournalManifestBinding(
	journal publicationJournal,
	prior *committedManifest,
	next *committedManifest,
	requireExactUnion bool,
) error {
	journalByPath := make(map[string]journalFile, len(journal.Files))
	for _, file := range journal.Files {
		journalByPath[file.Path] = file
	}
	priorPaths := make(map[string]struct{})
	if prior != nil {
		for _, file := range prior.Files {
			mode, err := parseManifestMode(file.Mode)
			if err != nil {
				return err
			}
			entry, exists := journalByPath[file.Path]
			if !exists || !entry.Prior.Present || !entry.Prior.Owned || entry.Prior.SHA256 != file.SHA256 || entry.Prior.Mode != mode {
				return fmt.Errorf("prior file %s differs from journal", file.Path)
			}
			priorPaths[file.Path] = struct{}{}
		}
	} else if journal.PriorManifest.Present && requireExactUnion {
		return fmt.Errorf("prior manifest binding is missing")
	}
	nextPaths := make(map[string]struct{})
	if next != nil {
		if next.SnapshotSHA256 != journal.SnapshotSHA256 {
			return fmt.Errorf("next snapshot digest differs from journal")
		}
		for _, file := range next.Files {
			mode, err := parseManifestMode(file.Mode)
			if err != nil {
				return err
			}
			entry, exists := journalByPath[file.Path]
			if !exists || !entry.Next.Present || !entry.Next.Owned || entry.Next.SHA256 != file.SHA256 || entry.Next.Mode != mode {
				return fmt.Errorf("next file %s differs from journal", file.Path)
			}
			nextPaths[file.Path] = struct{}{}
		}
	}
	for _, entry := range journal.Files {
		_, inPrior := priorPaths[entry.Path]
		_, inNext := nextPaths[entry.Path]
		if prior != nil && entry.Prior.Owned != inPrior {
			return fmt.Errorf("prior ownership for %s differs from manifest", entry.Path)
		}
		if next != nil && entry.Next.Present != inNext {
			return fmt.Errorf("next ownership for %s differs from manifest", entry.Path)
		}
		if requireExactUnion && !inPrior && !inNext {
			return fmt.Errorf("journal file %s is absent from both manifests", entry.Path)
		}
	}
	return nil
}

func verifyCompleteNextState(root *publicationRoot, journal publicationJournal) error {
	if err := verifyManifestState(root, journal.NextManifest); err != nil {
		return fmt.Errorf("%w: committed manifest proof failed", ErrPublicationRecoveryRequired)
	}
	for _, directory := range journal.Directories {
		present, err := root.observeDirectory(directory.Path)
		if err != nil || !present {
			return fmt.Errorf("%w: committed directory %s is ambiguous", ErrPublicationRecoveryRequired, directory.Path)
		}
	}
	for _, entry := range journal.Files {
		observed, _, err := root.observe(entry.Path, maximumPublicationFileBytes)
		if err != nil || observed.present != entry.Next.Present || (entry.Next.Present && (observed.sha256 != entry.Next.SHA256 || observed.mode != entry.Next.Mode)) {
			return fmt.Errorf("%w: committed file %s is ambiguous", ErrPublicationRecoveryRequired, entry.Path)
		}
	}
	return nil
}

func rollbackPublication(root *publicationRoot, tx *publicationTransaction, journal publicationJournal) error {
	parents, err := retainPublicationParents(root, journal)
	if err != nil {
		return fmt.Errorf("%w: retain rollback target directories: %v", ErrPublicationRecoveryRequired, err)
	}
	defer parents.close()
	for index := len(journal.Files) - 1; index >= 0; index-- {
		entry := journal.Files[index]
		if fileStatesEqual(entry.Prior, entry.Next) {
			if err := verifyRecoveryTargetRetained(root, parents, entry.Path, entry.Prior); err != nil {
				return err
			}
			continue
		}
		target, _, err := parents.observe(root, entry.Path, maximumPublicationFileBytes)
		if err != nil {
			return fmt.Errorf("%w: inspect rollback target %s: %v", ErrPublicationRecoveryRequired, entry.Path, err)
		}
		backup, backupErr := observeRecoveryBackup(tx, index, maximumPublicationFileBytes)
		if backupErr != nil {
			return fmt.Errorf("%w: inspect rollback backup %s: %v", ErrPublicationRecoveryRequired, entry.Path, backupErr)
		}
		quarantine, quarantineErr := observeRecoveryQuarantine(tx, index, maximumPublicationFileBytes)
		if quarantineErr != nil {
			return fmt.Errorf("%w: inspect rollback quarantine %s: %v", ErrPublicationRecoveryRequired, entry.Path, quarantineErr)
		}
		if quarantine.present && (!entry.Next.Present || quarantine.sha256 != entry.Next.SHA256 || quarantine.mode != entry.Next.Mode) {
			return fmt.Errorf("%w: rollback quarantine %s differs", ErrPublicationRecoveryRequired, entry.Path)
		}
		if entry.Prior.Present {
			if target.present && target.sha256 == entry.Prior.SHA256 && target.mode == entry.Prior.Mode && !backup.present {
				continue
			}
			if !entry.Prior.Owned || tx == nil || !backup.present || backup.sha256 != entry.Prior.SHA256 || backup.mode != entry.Prior.Mode {
				return fmt.Errorf("%w: prior file %s cannot be restored", ErrPublicationRecoveryRequired, entry.Path)
			}
			if target.present {
				if !entry.Next.Present || target.sha256 != entry.Next.SHA256 || target.mode != entry.Next.Mode {
					return fmt.Errorf("%w: rollback target %s was modified", ErrPublicationRecoveryRequired, entry.Path)
				}
				if err := rejectAmbiguousUninstalledTarget(tx, entry.Path, entry.Next, root.device); err != nil {
					return err
				}
				if err := root.quarantineTarget(entry.Path, tx, index, entry.Next, parents); err != nil {
					return fmt.Errorf("%w: quarantine new target %s: %v", ErrPublicationRecoveryRequired, entry.Path, err)
				}
			} else if entry.Next.Present && !quarantine.present {
				// The crash may have happened after the prior file moved to its
				// backup but before the staged replacement was installed.
			}
			if err := root.restoreBackup(entry.Path, tx, index, parents); err != nil {
				return fmt.Errorf("%w: restore target %s: %v", ErrPublicationRecoveryRequired, entry.Path, err)
			}
			if err := verifyRecoveryTargetRetained(root, parents, entry.Path, entry.Prior); err != nil {
				return err
			}
			continue
		}
		if backup.present {
			return fmt.Errorf("%w: new file %s has unexpected backup", ErrPublicationRecoveryRequired, entry.Path)
		}
		if target.present {
			if !entry.Next.Present || target.sha256 != entry.Next.SHA256 || target.mode != entry.Next.Mode {
				return fmt.Errorf("%w: new rollback target %s was modified", ErrPublicationRecoveryRequired, entry.Path)
			}
			if err := rejectAmbiguousUninstalledTarget(tx, entry.Path, entry.Next, root.device); err != nil {
				return err
			}
			if err := root.quarantineTarget(entry.Path, tx, index, entry.Next, parents); err != nil {
				return fmt.Errorf("%w: quarantine new target %s: %v", ErrPublicationRecoveryRequired, entry.Path, err)
			}
		}
	}
	if err := parents.close(); err != nil {
		return fmt.Errorf("%w: release rollback target directories: %v", ErrPublicationRecoveryRequired, err)
	}
	for index := len(journal.Directories) - 1; index >= 0; index-- {
		directory := journal.Directories[index]
		if directory.PriorPresent {
			present, err := root.observeDirectory(directory.Path)
			if err != nil || !present {
				return fmt.Errorf("%w: prior directory %s proof failed", ErrPublicationRecoveryRequired, directory.Path)
			}
			continue
		}
		if err := root.rollbackCreatedDirectory(directory.Path, journal.TransactionID, tx, index); err != nil {
			return fmt.Errorf("%w: rollback created directory %s: %v", ErrPublicationRecoveryRequired, directory.Path, err)
		}
	}

	manifestIndex := len(journal.Files)
	manifest, _, err := root.observe(generatedManifestRelativePath, maximumPublicationJournalBytes)
	if err != nil {
		return fmt.Errorf("%w: inspect rollback manifest: %v", ErrPublicationRecoveryRequired, err)
	}
	manifestBackup, backupErr := observeRecoveryBackup(tx, manifestIndex, maximumPublicationJournalBytes)
	if backupErr != nil {
		return fmt.Errorf("%w: inspect rollback manifest backup: %v", ErrPublicationRecoveryRequired, backupErr)
	}
	if journal.PriorManifest.Present {
		if manifest.present && manifest.sha256 == journal.PriorManifest.SHA256 && !manifestBackup.present {
			return verifyCompletePriorState(root, journal)
		}
		if tx == nil || !manifestBackup.present || manifestBackup.sha256 != journal.PriorManifest.SHA256 {
			return fmt.Errorf("%w: prior manifest cannot be restored", ErrPublicationRecoveryRequired)
		}
		if manifest.present {
			return fmt.Errorf("%w: rollback manifest has unexpected bytes", ErrPublicationRecoveryRequired)
		}
		if err := root.restoreManifestBackup(tx, manifestIndex); err != nil {
			return fmt.Errorf("%w: restore prior manifest: %v", ErrPublicationRecoveryRequired, err)
		}
	} else {
		if manifestBackup.present {
			return fmt.Errorf("%w: first publication has manifest backup", ErrPublicationRecoveryRequired)
		}
		if manifest.present {
			return fmt.Errorf("%w: first publication rollback has manifest", ErrPublicationRecoveryRequired)
		}
	}
	return verifyCompletePriorState(root, journal)
}

func rejectAmbiguousUninstalledTarget(tx *publicationTransaction, relative string, expected journalFileState, device uint64) error {
	if tx == nil || tx.stage == nil || !expected.Present {
		return nil
	}
	parent, name, err := openParentAt(tx.stage, relative, device)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: inspect staged source %s: %v", ErrPublicationRecoveryRequired, relative, err)
	}
	defer parent.Close()
	staged, _, err := observeRegularAt(parent, name, maximumPublicationFileBytes)
	if err != nil {
		return fmt.Errorf("%w: inspect staged source %s: %v", ErrPublicationRecoveryRequired, relative, err)
	}
	if !staged.present {
		return nil
	}
	if staged.sha256 != expected.SHA256 || staged.mode != expected.Mode {
		return fmt.Errorf("%w: staged source %s differs", ErrPublicationRecoveryRequired, relative)
	}
	return fmt.Errorf("%w: target %s matches next bytes while its staged source still exists", ErrPublicationRecoveryRequired, relative)
}

func verifyCompletePriorState(root *publicationRoot, journal publicationJournal) error {
	if err := verifyManifestState(root, journal.PriorManifest); err != nil {
		return fmt.Errorf("%w: prior manifest proof failed", ErrPublicationRecoveryRequired)
	}
	for _, directory := range journal.Directories {
		present, err := root.observeDirectory(directory.Path)
		if err != nil || present != directory.PriorPresent {
			return fmt.Errorf("%w: prior directory %s proof failed", ErrPublicationRecoveryRequired, directory.Path)
		}
	}
	for _, entry := range journal.Files {
		if err := verifyRecoveryTarget(root, entry.Path, entry.Prior); err != nil {
			return err
		}
	}
	return nil
}

func verifyRecoveryTarget(root *publicationRoot, relative string, expected journalFileState) error {
	observed, _, err := root.observe(relative, maximumPublicationFileBytes)
	if err != nil || observed.present != expected.Present || (expected.Present && (observed.sha256 != expected.SHA256 || observed.mode != expected.Mode)) {
		return fmt.Errorf("%w: prior file %s proof failed", ErrPublicationRecoveryRequired, relative)
	}
	return nil
}

func verifyRecoveryTargetRetained(
	root *publicationRoot,
	parents *retainedPublicationParents,
	relative string,
	expected journalFileState,
) error {
	observed, _, err := parents.observe(root, relative, maximumPublicationFileBytes)
	if err != nil || observed.present != expected.Present || (expected.Present && (observed.sha256 != expected.SHA256 || observed.mode != expected.Mode)) {
		return fmt.Errorf("%w: prior file %s proof failed", ErrPublicationRecoveryRequired, relative)
	}
	return nil
}

func observeRecoveryBackup(tx *publicationTransaction, index int, maximum int64) (observedFile, error) {
	if tx == nil || tx.backup == nil {
		return observedFile{}, nil
	}
	observed, _, err := observeRegularAt(tx.backup, publicationBackupName(index), maximum)
	return observed, err
}

func observeRecoveryQuarantine(tx *publicationTransaction, index int, maximum int64) (observedFile, error) {
	if tx == nil || tx.backup == nil {
		return observedFile{}, nil
	}
	observed, _, err := observeRegularAt(tx.backup, publicationQuarantineName(index), maximum)
	return observed, err
}
