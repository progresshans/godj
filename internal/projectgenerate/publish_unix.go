//go:build darwin || linux

package projectgenerate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	maximumPublicationFileBytes          = 64 << 20
	maximumPublicationTransactionEntries = maximumPublicationJournalFiles * 4
)

type fileIdentity struct {
	device uint64
	inode  uint64
}

type observedFile struct {
	present  bool
	sha256   string
	mode     uint32
	identity fileIdentity
}

type publicationRoot struct {
	absolute     string
	directory    *os.File
	identity     fileIdentity
	device       uint64
	godj         *os.File
	godjIdentity fileIdentity
	transactions *os.File
	txIdentity   fileIdentity
	lockIdentity fileIdentity
	lockHeld     bool
}

type publicationTransaction struct {
	id       string
	absolute string
	root     *os.File
	rootID   fileIdentity
	stage    *os.File
	stageID  fileIdentity
	backup   *os.File
	backupID fileIdentity
}

type retainedPublicationParent struct {
	relative  string
	directory *os.File
	identity  fileIdentity
}

type retainedPublicationParents struct {
	byRelative map[string]*retainedPublicationParent
}

type transactionCleanupEntry struct {
	directory bool
	sha256    string
	mode      uint32
}

func openPublicationRoot(root string) (*publicationRoot, error) {
	return openPublicationRootAuthority(ProjectRoot{absolute: root})
}

func openPublicationRootAuthority(root ProjectRoot) (*publicationRoot, error) {
	sealed, err := resolveProjectRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open publication root: %w", err)
	}
	if sealed.absolute == "" {
		return nil, fmt.Errorf("open publication root: empty path")
	}
	absolute := sealed.absolute
	var initial unix.Stat_t
	if err := unix.Lstat(absolute, &initial); err != nil {
		return nil, fmt.Errorf("open publication root: %w", err)
	}
	if !statIsDirectory(&initial) || uint64(initial.Dev) != sealed.device || uint64(initial.Ino) != sealed.inode {
		return nil, fmt.Errorf("open publication root: path is not a physical directory")
	}
	fd, err := unix.Open(absolute, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open publication root: %w", err)
	}
	directory := os.NewFile(uintptr(fd), absolute)
	if directory == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open publication root: retain directory")
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !statIsDirectory(&opened) || identityOf(&opened) != identityOf(&initial) {
		_ = directory.Close()
		if err == nil {
			err = errors.New("directory identity changed")
		}
		return nil, fmt.Errorf("open publication root: %w", err)
	}
	result := &publicationRoot{
		absolute:  absolute,
		directory: directory,
		identity:  identityOf(&opened),
		device:    uint64(opened.Dev),
	}
	result.godj, _, err = ensureDirectoryAt(result.directory, ".godj", 0o700, result.device)
	if err != nil {
		_ = result.close()
		return nil, fmt.Errorf("open publication metadata directory: %w", err)
	}
	result.transactions, _, err = ensureDirectoryAt(result.godj, "transactions", 0o700, result.device)
	if err != nil {
		_ = result.close()
		return nil, fmt.Errorf("open publication transaction directory: %w", err)
	}
	result.godjIdentity, err = retainedDirectoryIdentity(result.godj)
	if err == nil {
		result.txIdentity, err = retainedDirectoryIdentity(result.transactions)
	}
	if err != nil || !result.verify() {
		_ = result.close()
		return nil, fmt.Errorf("open publication root: retained path changed")
	}
	return result, nil
}

func (root *publicationRoot) close() error {
	if root == nil {
		return nil
	}
	var failures []error
	for _, file := range []*os.File{root.transactions, root.godj, root.directory} {
		if file != nil {
			failures = append(failures, file.Close())
		}
	}
	root.transactions = nil
	root.godj = nil
	root.directory = nil
	return errors.Join(failures...)
}

func (root *publicationRoot) verify() bool {
	if root == nil || root.directory == nil {
		return false
	}
	var retained unix.Stat_t
	if err := unix.Fstat(int(root.directory.Fd()), &retained); err != nil || !statIsDirectory(&retained) || identityOf(&retained) != root.identity {
		return false
	}
	var current unix.Stat_t
	if unix.Lstat(root.absolute, &current) != nil || !statIsDirectory(&current) || identityOf(&current) != root.identity {
		return false
	}
	if !directoryBindingMatches(root.directory, ".godj", root.godj, root.godjIdentity) ||
		!directoryBindingMatches(root.godj, "transactions", root.transactions, root.txIdentity) {
		return false
	}
	if root.lockHeld {
		var lock unix.Stat_t
		if unix.Fstatat(int(root.godj.Fd()), path.Base(publicationLockRelativePath), &lock, unix.AT_SYMLINK_NOFOLLOW) != nil ||
			!statIsRegular(&lock) || identityOf(&lock) != root.lockIdentity {
			return false
		}
	}
	return true
}

func (root *publicationRoot) createTransaction(id string) (*publicationTransaction, error) {
	if !validTransactionID(id) {
		return nil, fmt.Errorf("create publication transaction: invalid id")
	}
	if err := unix.Mkdirat(int(root.transactions.Fd()), id, 0o700); err != nil {
		return nil, fmt.Errorf("create publication transaction: %w", err)
	}
	if err := syncDirectory(root.transactions); err != nil {
		_ = unix.Unlinkat(int(root.transactions.Fd()), id, unix.AT_REMOVEDIR)
		return nil, fmt.Errorf("sync publication transaction directory: %w", err)
	}
	txRoot, _, err := openDirectoryAt(root.transactions, id, root.device)
	if err != nil {
		return nil, fmt.Errorf("open publication transaction: %w", err)
	}
	tx := &publicationTransaction{
		id:       id,
		absolute: filepath.Join(root.absolute, filepath.FromSlash(publicationTransactionDirectoryPath), id),
		root:     txRoot,
	}
	tx.rootID, err = retainedDirectoryIdentity(tx.root)
	if err != nil {
		_ = tx.close()
		return nil, fmt.Errorf("retain publication transaction: %w", err)
	}
	tx.stage, _, err = ensureDirectoryAt(tx.root, "stage", 0o700, root.device)
	if err == nil {
		tx.backup, _, err = ensureDirectoryAt(tx.root, "backup", 0o700, root.device)
	}
	if err != nil {
		_ = tx.close()
		return nil, fmt.Errorf("prepare publication transaction: %w", err)
	}
	tx.stageID, err = retainedDirectoryIdentity(tx.stage)
	if err == nil {
		tx.backupID, err = retainedDirectoryIdentity(tx.backup)
	}
	if err != nil {
		_ = tx.close()
		return nil, fmt.Errorf("retain publication transaction directories: %w", err)
	}
	return tx, nil
}

func (tx *publicationTransaction) close() error {
	if tx == nil {
		return nil
	}
	var failures []error
	for _, file := range []*os.File{tx.backup, tx.stage, tx.root} {
		if file != nil {
			failures = append(failures, file.Close())
		}
	}
	tx.backup = nil
	tx.stage = nil
	tx.root = nil
	return errors.Join(failures...)
}

func (tx *publicationTransaction) stageRoot(root *publicationRoot) (string, error) {
	if !root.verify() || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) ||
		!directoryBindingMatches(tx.root, "stage", tx.stage, tx.stageID) ||
		!directoryBindingMatches(tx.root, "backup", tx.backup, tx.backupID) {
		return "", fmt.Errorf("publication transaction path changed")
	}
	stagePath := filepath.Join(tx.absolute, "stage")
	var pathStat unix.Stat_t
	if err := unix.Lstat(stagePath, &pathStat); err != nil || !statIsDirectory(&pathStat) || identityOf(&pathStat) != tx.stageID {
		return "", fmt.Errorf("publication stage path changed")
	}
	return stagePath, nil
}

func (tx *publicationTransaction) writeStageFile(relative string, contents []byte, mode uint32, device uint64) error {
	parent, name, err := openOrCreateParentAt(tx.stage, relative, device)
	if err != nil {
		return err
	}
	defer parent.Close()
	return writeExclusiveRegularAt(parent, name, contents, mode)
}

func (root *publicationRoot) observe(relative string, maximum int64) (observedFile, []byte, error) {
	if path.Dir(relative) == ".godj" {
		if !root.verify() {
			return observedFile{}, nil, fmt.Errorf("publication metadata directory changed")
		}
		return observeRegularAt(root.godj, path.Base(relative), maximum)
	}
	parent, name, err := root.openParent(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return observedFile{}, nil, nil
		}
		return observedFile{}, nil, err
	}
	defer parent.Close()
	return observeRegularAt(parent, name, maximum)
}

func (root *publicationRoot) observeDirectory(relative string) (bool, error) {
	if err := validatePublicationRelativePath(relative); err != nil {
		return false, err
	}
	parts := strings.Split(relative, "/")
	current, err := duplicateDirectory(root.directory, root.device)
	if err != nil {
		return false, err
	}
	for _, component := range parts {
		next, _, openErr := openDirectoryAt(current, component, root.device)
		if errors.Is(openErr, os.ErrNotExist) {
			_ = current.Close()
			return false, nil
		}
		if openErr != nil {
			_ = current.Close()
			return false, openErr
		}
		_ = current.Close()
		current = next
	}
	_ = current.Close()
	return true, nil
}

func (root *publicationRoot) createTargetDirectory(
	relative string,
	tx *publicationTransaction,
	index int,
	parents *retainedPublicationParents,
) error {
	if tx == nil || tx.root == nil || !validTransactionID(tx.id) {
		return fmt.Errorf("create target directory: invalid transaction")
	}
	parent, name, err := parents.forTarget(root, relative)
	if err != nil {
		return err
	}
	if err := requireAbsentAt(parent.directory, name); err != nil {
		return err
	}
	preparedName := publicationDirectoryStageName(index)
	if err := requireAbsentAt(tx.root, preparedName); err != nil {
		return err
	}
	if err := unix.Mkdirat(int(tx.root.Fd()), preparedName, 0o755); err != nil {
		return err
	}
	if err := syncDirectory(tx.root); err != nil {
		return err
	}
	prepared, _, err := openDirectoryAt(tx.root, preparedName, root.device)
	if err != nil {
		return err
	}
	defer prepared.Close()
	if err := prepared.Chmod(0o755); err != nil {
		return err
	}
	if err := prepared.Sync(); err != nil {
		return err
	}
	marker := publicationDirectoryMarkerName(tx.id)
	if err := writeExclusiveRegularAt(prepared, marker, publicationDirectoryMarkerContents(tx.id, relative), 0o600); err != nil {
		return err
	}
	if !root.verify() || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		return fmt.Errorf("create target directory: publication path changed")
	}
	if err := requireAbsentAt(parent.directory, name); err != nil {
		return err
	}
	if !parent.bindingMatches(root) {
		return fmt.Errorf("create target directory: target parent changed")
	}
	if err := renameNoReplace(int(tx.root.Fd()), preparedName, int(parent.directory.Fd()), name); err != nil {
		return err
	}
	if err := errors.Join(syncDirectory(tx.root), syncDirectory(parent.directory)); err != nil {
		return err
	}
	if err := parents.retainIfPresent(root, relative); err != nil {
		return fmt.Errorf("retain created target directory: %w", err)
	}
	if _, err := parents.require(root, relative); err != nil {
		return err
	}
	return nil
}

func (root *publicationRoot) cleanupCreatedDirectoryMarker(relative, transactionID string) error {
	parent, name, err := root.openParent(relative)
	if err != nil {
		return err
	}
	defer parent.Close()
	directory, stat, err := openDirectoryAt(parent, name, root.device)
	if err != nil {
		return err
	}
	defer directory.Close()
	if uint32(stat.Mode)&0o7777 != 0o755 {
		return fmt.Errorf("created directory mode differs")
	}
	marker := publicationDirectoryMarkerName(transactionID)
	observed, contents, err := observeRegularAt(directory, marker, int64(maximumPublicationPathBytes+64))
	if err != nil {
		return err
	}
	if !observed.present {
		return nil
	}
	if string(contents) != string(publicationDirectoryMarkerContents(transactionID, relative)) || observed.mode != 0o600 {
		return fmt.Errorf("created directory marker differs")
	}
	if err := unix.Unlinkat(int(directory.Fd()), marker, 0); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func (root *publicationRoot) rollbackCreatedDirectory(
	relative, transactionID string,
	tx *publicationTransaction,
	index int,
) error {
	if tx == nil || tx.root == nil || tx.id != transactionID {
		return fmt.Errorf("created directory transaction is unavailable")
	}
	parent, name, err := root.openParent(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer parent.Close()
	directory, stat, err := openDirectoryAt(parent, name, root.device)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	if uint32(stat.Mode)&0o7777 != 0o755 {
		return fmt.Errorf("created directory mode differs")
	}
	marker := publicationDirectoryMarkerName(transactionID)
	observed, contents, err := observeRegularAt(directory, marker, int64(maximumPublicationPathBytes+64))
	if err != nil || !observed.present || observed.mode != 0o600 || string(contents) != string(publicationDirectoryMarkerContents(transactionID, relative)) {
		return fmt.Errorf("created directory marker is not exact")
	}
	handle, err := duplicateDirectory(directory, root.device)
	if err != nil {
		return err
	}
	entries, readErr := handle.ReadDir(2)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := handle.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if len(entries) != 1 || entries[0].Name() != marker {
		return fmt.Errorf("created directory is not empty")
	}
	if !directoryBindingMatches(parent, name, directory, identityOf(&stat)) {
		return fmt.Errorf("created directory identity changed")
	}
	if !root.verify() || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		return fmt.Errorf("created directory transaction path changed")
	}
	prepared := publicationDirectoryStageName(index)
	if err := requireAbsentAt(tx.root, prepared); err != nil {
		return err
	}
	// Move the exact marker-bearing directory back under the durable
	// transaction authority in one rename. A crash can therefore expose either
	// the old target binding or the prepared transaction member, both of which
	// are recoverable from the journal; it can never strand a marker-free target
	// directory between unlink and rmdir operations.
	if err := renameNoReplace(int(parent.Fd()), name, int(tx.root.Fd()), prepared); err != nil {
		return err
	}
	return errors.Join(syncDirectory(parent), syncDirectory(tx.root))
}

func publicationDirectoryMarkerName(transactionID string) string {
	return ".godj-publication-" + transactionID
}

func publicationDirectoryStageName(index int) string {
	return fmt.Sprintf("directory-%06d.new", index)
}

func publicationDirectoryMarkerContents(transactionID, relative string) []byte {
	return []byte(transactionID + "\n" + relative + "\n")
}

func retainPublicationParents(root *publicationRoot, journal publicationJournal) (*retainedPublicationParents, error) {
	parents := &retainedPublicationParents{byRelative: make(map[string]*retainedPublicationParent)}
	wanted := map[string]struct{}{".": {}}
	for _, directory := range journal.Directories {
		wanted[path.Dir(directory.Path)] = struct{}{}
	}
	for _, file := range journal.Files {
		wanted[path.Dir(file.Path)] = struct{}{}
	}
	relatives := make([]string, 0, len(wanted))
	for relative := range wanted {
		relatives = append(relatives, relative)
	}
	sort.Slice(relatives, func(left, right int) bool {
		return publicationDirectoryLess(relatives[left], relatives[right])
	})
	for _, relative := range relatives {
		if err := parents.retainIfPresent(root, relative); err != nil {
			_ = parents.close()
			return nil, err
		}
	}
	return parents, nil
}

func (parents *retainedPublicationParents) close() error {
	if parents == nil {
		return nil
	}
	var failures []error
	for relative, parent := range parents.byRelative {
		if parent != nil && parent.directory != nil {
			failures = append(failures, parent.directory.Close())
			parent.directory = nil
		}
		delete(parents.byRelative, relative)
	}
	return errors.Join(failures...)
}

func (parents *retainedPublicationParents) retainIfPresent(root *publicationRoot, relative string) error {
	if parents == nil {
		return fmt.Errorf("retain publication parent: nil set")
	}
	if _, exists := parents.byRelative[relative]; exists {
		return nil
	}
	directory, err := root.openDirectoryPath(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	identity, err := retainedDirectoryIdentity(directory)
	if err != nil {
		_ = directory.Close()
		return err
	}
	parent := &retainedPublicationParent{relative: relative, directory: directory, identity: identity}
	if !parent.bindingMatches(root) {
		_ = directory.Close()
		return fmt.Errorf("retain publication parent: %s changed", relative)
	}
	parents.byRelative[relative] = parent
	return nil
}

func (parents *retainedPublicationParents) require(root *publicationRoot, relative string) (*retainedPublicationParent, error) {
	if parents == nil {
		return nil, fmt.Errorf("publication parent set is nil")
	}
	parent := parents.byRelative[relative]
	if parent == nil || parent.directory == nil || !parent.bindingMatches(root) {
		return nil, fmt.Errorf("publication parent %s changed", relative)
	}
	return parent, nil
}

func (parents *retainedPublicationParents) forTarget(root *publicationRoot, relative string) (*retainedPublicationParent, string, error) {
	if err := validatePublicationRelativePath(relative); err != nil {
		return nil, "", err
	}
	parent, err := parents.require(root, path.Dir(relative))
	if err != nil {
		return nil, "", err
	}
	return parent, path.Base(relative), nil
}

func (parents *retainedPublicationParents) observe(
	root *publicationRoot,
	relative string,
	maximum int64,
) (observedFile, []byte, error) {
	if parents == nil || parents.byRelative[path.Dir(relative)] == nil {
		return root.observe(relative, maximum)
	}
	parent, name, err := parents.forTarget(root, relative)
	if err != nil {
		return observedFile{}, nil, err
	}
	return observeRegularAt(parent.directory, name, maximum)
}

func (parent *retainedPublicationParent) bindingMatches(root *publicationRoot) bool {
	if parent == nil || parent.directory == nil || !root.verify() {
		return false
	}
	var retained unix.Stat_t
	if err := unix.Fstat(int(parent.directory.Fd()), &retained); err != nil || !statIsDirectory(&retained) || identityOf(&retained) != parent.identity {
		return false
	}
	if parent.relative == "." {
		return parent.identity == root.identity
	}
	canonicalParent, name, err := root.openParent(parent.relative)
	if err != nil {
		return false
	}
	defer canonicalParent.Close()
	return directoryBindingMatches(canonicalParent, name, parent.directory, parent.identity)
}

func (root *publicationRoot) openDirectoryPath(relative string) (*os.File, error) {
	if relative == "." {
		return duplicateDirectory(root.directory, root.device)
	}
	parent, name, err := root.openParent(relative)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	directory, _, err := openDirectoryAt(parent, name, root.device)
	return directory, err
}

func (root *publicationRoot) openParent(relative string) (*os.File, string, error) {
	if err := validatePublicationRelativePath(relative); err != nil {
		return nil, "", err
	}
	parts := strings.Split(relative, "/")
	parent, err := duplicateDirectory(root.directory, root.device)
	if err != nil {
		return nil, "", err
	}
	for _, component := range parts[:len(parts)-1] {
		next, _, openErr := openDirectoryAt(parent, component, root.device)
		_ = parent.Close()
		if openErr != nil {
			return nil, "", openErr
		}
		parent = next
	}
	return parent, parts[len(parts)-1], nil
}

func (root *publicationRoot) renameManifestToBackup(tx *publicationTransaction, index int) error {
	if !root.verify() || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		return fmt.Errorf("rename manifest to backup: retained path changed")
	}
	name := path.Base(generatedManifestRelativePath)
	backup := publicationBackupName(index)
	if err := requireAbsentAt(tx.backup, backup); err != nil {
		return err
	}
	if err := renameNoReplace(int(root.godj.Fd()), name, int(tx.backup.Fd()), backup); err != nil {
		return err
	}
	return errors.Join(syncDirectory(root.godj), syncDirectory(tx.backup))
}

func (root *publicationRoot) renameTargetToBackupRetained(
	relative string,
	tx *publicationTransaction,
	index int,
	parents *retainedPublicationParents,
) error {
	parent, name, err := parents.forTarget(root, relative)
	if err != nil {
		return err
	}
	backup := publicationBackupName(index)
	if err := requireAbsentAt(tx.backup, backup); err != nil {
		return err
	}
	if !parent.bindingMatches(root) || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		return fmt.Errorf("rename target to backup: retained path changed")
	}
	if err := renameNoReplace(int(parent.directory.Fd()), name, int(tx.backup.Fd()), backup); err != nil {
		return err
	}
	return errors.Join(syncDirectory(parent.directory), syncDirectory(tx.backup))
}

func (root *publicationRoot) renameStageManifestToTarget(tx *publicationTransaction) (*os.File, error) {
	stageParent, stageName, err := openParentAt(tx.stage, generatedManifestRelativePath, root.device)
	if err != nil {
		return nil, err
	}
	if !root.verify() || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		_ = stageParent.Close()
		return nil, fmt.Errorf("rename staged manifest: retained path changed")
	}
	if err := renameNoReplace(int(stageParent.Fd()), stageName, int(root.godj.Fd()), path.Base(generatedManifestRelativePath)); err != nil {
		_ = stageParent.Close()
		return nil, err
	}
	return stageParent, nil
}

func (root *publicationRoot) renameStageToTargetRetained(
	relative string,
	tx *publicationTransaction,
	parents *retainedPublicationParents,
) error {
	stageParent, stageName, err := openParentAt(tx.stage, relative, root.device)
	if err != nil {
		return err
	}
	defer stageParent.Close()
	targetParent, targetName, err := parents.forTarget(root, relative)
	if err != nil {
		return err
	}
	if !targetParent.bindingMatches(root) || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		return fmt.Errorf("rename stage to target: retained path changed")
	}
	if err := renameNoReplace(int(stageParent.Fd()), stageName, int(targetParent.directory.Fd()), targetName); err != nil {
		return err
	}
	return errors.Join(syncDirectory(stageParent), syncDirectory(targetParent.directory))
}

func (root *publicationRoot) restoreBackup(
	relative string,
	tx *publicationTransaction,
	index int,
	parents *retainedPublicationParents,
) error {
	targetParent, targetName, err := parents.forTarget(root, relative)
	if err != nil {
		return err
	}
	backup := publicationBackupName(index)
	if !targetParent.bindingMatches(root) || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		return fmt.Errorf("restore backup: retained path changed")
	}
	if err := renameNoReplace(int(tx.backup.Fd()), backup, int(targetParent.directory.Fd()), targetName); err != nil {
		return err
	}
	return errors.Join(syncDirectory(tx.backup), syncDirectory(targetParent.directory))
}

func (root *publicationRoot) restoreManifestBackup(tx *publicationTransaction, index int) error {
	if !root.verify() || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		return fmt.Errorf("restore manifest backup: retained path changed")
	}
	backup := publicationBackupName(index)
	if err := renameNoReplace(int(tx.backup.Fd()), backup, int(root.godj.Fd()), path.Base(generatedManifestRelativePath)); err != nil {
		return err
	}
	return errors.Join(syncDirectory(tx.backup), syncDirectory(root.godj))
}

func (root *publicationRoot) quarantineTarget(
	relative string,
	tx *publicationTransaction,
	index int,
	expected journalFileState,
	parents *retainedPublicationParents,
) error {
	if tx == nil || tx.backup == nil || !expected.Present {
		return fmt.Errorf("quarantine target: invalid transaction or expected state")
	}
	parent, name, err := parents.forTarget(root, relative)
	if err != nil {
		return err
	}
	quarantine := publicationQuarantineName(index)
	if err := requireAbsentAt(tx.backup, quarantine); err != nil {
		return err
	}
	if !parent.bindingMatches(root) || !directoryBindingMatches(root.transactions, tx.id, tx.root, tx.rootID) {
		return fmt.Errorf("quarantine target: retained path changed")
	}
	if err := renameNoReplace(int(parent.directory.Fd()), name, int(tx.backup.Fd()), quarantine); err != nil {
		return err
	}
	if err := errors.Join(syncDirectory(parent.directory), syncDirectory(tx.backup)); err != nil {
		return err
	}
	observed, _, observeErr := observeRegularAt(tx.backup, quarantine, maximumPublicationFileBytes)
	if observeErr == nil && observed.present && observed.sha256 == expected.SHA256 && observed.mode == expected.Mode {
		return nil
	}
	if absentErr := requireAbsentAt(parent.directory, name); absentErr != nil {
		return fmt.Errorf("quarantine target: target changed while validating: %v", absentErr)
	}
	if restoreErr := renameNoReplace(int(tx.backup.Fd()), quarantine, int(parent.directory.Fd()), name); restoreErr != nil {
		return fmt.Errorf("quarantine target differs and restore failed: %v", restoreErr)
	}
	_ = errors.Join(syncDirectory(tx.backup), syncDirectory(parent.directory))
	if observeErr != nil {
		return fmt.Errorf("quarantine target validation failed: %v", observeErr)
	}
	return fmt.Errorf("quarantine target differs from journal")
}

func (root *publicationRoot) writeJournal(tx *publicationTransaction, document []byte) error {
	const temporary = "journal.tmp"
	if err := writeExclusiveRegularAt(tx.root, temporary, document, 0o600); err != nil {
		return err
	}
	journalName := filepath.Base(publicationJournalRelativePath)
	if err := requireAbsentAt(root.godj, journalName); err != nil {
		return err
	}
	if err := renameNoReplace(int(tx.root.Fd()), temporary, int(root.godj.Fd()), journalName); err != nil {
		return err
	}
	return errors.Join(syncDirectory(tx.root), syncDirectory(root.godj))
}

func (root *publicationRoot) removeJournal(expected observedFile) error {
	name := filepath.Base(publicationJournalRelativePath)
	current, _, err := observeRegularAt(root.godj, name, maximumPublicationJournalBytes)
	if err != nil || !current.present || current.sha256 != expected.sha256 || current.mode != expected.mode || current.identity != expected.identity {
		return fmt.Errorf("publication journal changed before removal")
	}
	if err := unix.Unlinkat(int(root.godj.Fd()), name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return syncDirectory(root.godj)
}

func (root *publicationRoot) removeTransaction(tx *publicationTransaction, journal *publicationJournal) error {
	return root.removeTransactionWithHooks(tx, journal, publicationHooks{})
}

func (root *publicationRoot) removeTransactionWithHooks(
	tx *publicationTransaction,
	journal *publicationJournal,
	hooks publicationHooks,
) error {
	if tx == nil || !validTransactionID(tx.id) || tx.root == nil {
		return fmt.Errorf("remove publication transaction: invalid transaction")
	}
	id := tx.id
	rootID := tx.rootID
	var closeFailures []error
	for _, handle := range []*os.File{tx.backup, tx.stage} {
		if handle != nil {
			closeFailures = append(closeFailures, handle.Close())
		}
	}
	tx.backup = nil
	tx.stage = nil
	if err := errors.Join(closeFailures...); err != nil {
		return err
	}
	if !root.verify() || !directoryBindingMatches(root.transactions, id, tx.root, rootID) {
		return fmt.Errorf("remove publication transaction: project root changed")
	}
	roster, err := transactionCleanupRoster(tx.id, journal)
	if err != nil {
		return err
	}
	entriesSeen := 0
	if err := validateDirectoryContentsExact(tx.root, root.device, "", roster, &entriesSeen); err != nil {
		return err
	}
	entriesSeen = 0
	if err := removeDirectoryContentsExact(tx.root, root.device, "", roster, &entriesSeen, func(relative string) error {
		return hooks.fire(publicationStepTransactionEntry, relative, -1)
	}); err != nil {
		return err
	}
	if !directoryBindingMatches(root.transactions, id, tx.root, rootID) {
		return fmt.Errorf("remove publication transaction: transaction identity changed")
	}
	if err := tx.root.Close(); err != nil {
		return err
	}
	tx.root = nil
	if err := unix.Unlinkat(int(root.transactions.Fd()), id, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return syncDirectory(root.transactions)
}

func (root *publicationRoot) openTransaction(id string) (*publicationTransaction, error) {
	if !validTransactionID(id) {
		return nil, fmt.Errorf("open publication transaction: invalid id")
	}
	txRoot, _, err := openDirectoryAt(root.transactions, id, root.device)
	if err != nil {
		return nil, err
	}
	tx := &publicationTransaction{
		id:       id,
		absolute: filepath.Join(root.absolute, filepath.FromSlash(publicationTransactionDirectoryPath), id),
		root:     txRoot,
	}
	tx.rootID, err = retainedDirectoryIdentity(tx.root)
	if err != nil {
		_ = tx.close()
		return nil, err
	}
	tx.stage, err = openOptionalDirectoryAt(tx.root, "stage", root.device)
	if err == nil {
		tx.backup, err = openOptionalDirectoryAt(tx.root, "backup", root.device)
	}
	if err != nil {
		_ = tx.close()
		return nil, err
	}
	if tx.stage != nil {
		tx.stageID, err = retainedDirectoryIdentity(tx.stage)
	}
	if err == nil && tx.backup != nil {
		tx.backupID, err = retainedDirectoryIdentity(tx.backup)
	}
	if err != nil {
		_ = tx.close()
		return nil, err
	}
	return tx, nil
}

func (root *publicationRoot) removeOrphanTransaction(id string) error {
	if !validTransactionID(id) {
		return fmt.Errorf("remove orphan publication transaction: invalid id")
	}
	txRoot, _, err := openDirectoryAt(root.transactions, id, root.device)
	if err != nil {
		return err
	}
	rootID, err := retainedDirectoryIdentity(txRoot)
	if err != nil {
		_ = txRoot.Close()
		return err
	}
	tx := &publicationTransaction{
		id:       id,
		absolute: filepath.Join(root.absolute, filepath.FromSlash(publicationTransactionDirectoryPath), id),
		root:     txRoot,
		rootID:   rootID,
	}
	var journal *publicationJournal
	observed, document, observeErr := observeRegularAt(tx.root, "journal.tmp", maximumPublicationJournalBytes)
	if observeErr != nil {
		_ = tx.close()
		return observeErr
	}
	if observed.present {
		decoded, decodeErr := decodePublicationJournal(document)
		if decodeErr != nil {
			if observed.mode != 0o600 {
				_ = tx.close()
				return fmt.Errorf("orphan transaction journal is invalid")
			}
			roster, rosterErr := transactionCleanupRoster(tx.id, nil)
			if rosterErr != nil {
				_ = tx.close()
				return rosterErr
			}
			roster["journal.tmp"] = transactionCleanupEntry{sha256: observed.sha256, mode: observed.mode}
			entriesSeen := 0
			if validateErr := validateDirectoryContentsExact(tx.root, root.device, "", roster, &entriesSeen); validateErr != nil {
				_ = tx.close()
				return fmt.Errorf("orphan transaction journal is invalid: %w", validateErr)
			}
			if unlinkErr := unix.Unlinkat(int(tx.root.Fd()), "journal.tmp", 0); unlinkErr != nil {
				_ = tx.close()
				return unlinkErr
			}
			if syncErr := syncDirectory(tx.root); syncErr != nil {
				_ = tx.close()
				return syncErr
			}
			return root.removeTransaction(tx, nil)
		}
		if decoded.TransactionID != id {
			_ = tx.close()
			return fmt.Errorf("orphan transaction journal is invalid")
		}
		journal = &decoded
	}
	return root.removeTransaction(tx, journal)
}

func (root *publicationRoot) transactionNames() ([]string, error) {
	handle, err := duplicateDirectory(root.transactions, root.device)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	entries, err := handle.ReadDir(maximumPublicationJournalFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maximumPublicationJournalFiles {
		return nil, fmt.Errorf("publication transaction count exceeds limit")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !validTransactionID(entry.Name()) || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return nil, fmt.Errorf("unsafe publication transaction entry")
		}
		names = append(names, entry.Name())
	}
	return names, nil
}

func publicationBackupName(index int) string {
	return fmt.Sprintf("%06d.old", index)
}

func publicationQuarantineName(index int) string {
	return fmt.Sprintf("%06d.new", index)
}

func observeRegularAt(parent *os.File, name string, maximum int64) (observedFile, []byte, error) {
	var initial unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return observedFile{}, nil, nil
		}
		return observedFile{}, nil, err
	}
	if !statIsRegular(&initial) {
		return observedFile{}, nil, fmt.Errorf("entry is not a regular file")
	}
	if initial.Size < 0 || initial.Size > maximum {
		return observedFile{}, nil, fmt.Errorf("entry exceeds size limit")
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return observedFile{}, nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return observedFile{}, nil, fmt.Errorf("retain regular file")
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !statIsRegular(&opened) || identityOf(&opened) != identityOf(&initial) {
		_ = file.Close()
		if err == nil {
			err = errors.New("entry identity changed")
		}
		return observedFile{}, nil, err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	if readErr == nil {
		if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
			readErr = seekErr
		} else {
			second, secondErr := io.ReadAll(io.LimitReader(file, maximum+1))
			if secondErr != nil {
				readErr = secondErr
			} else if !bytes.Equal(contents, second) {
				readErr = errors.New("regular file changed while reading")
			}
		}
	}
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(contents)) > maximum {
		return observedFile{}, nil, errors.Join(readErr, closeErr, fmt.Errorf("read regular file"))
	}
	var after unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW); err != nil || !statIsRegular(&after) || identityOf(&after) != identityOf(&opened) || after.Size != int64(len(contents)) || after.Mode != opened.Mode {
		if err == nil {
			err = errors.New("entry identity changed after read")
		}
		return observedFile{}, nil, err
	}
	return observedFile{
		present:  true,
		sha256:   digestBytes(contents),
		mode:     uint32(after.Mode) & 0o7777,
		identity: identityOf(&after),
	}, contents, nil
}

func writeExclusiveRegularAt(parent *os.File, name string, contents []byte, mode uint32) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		_ = unix.Unlinkat(int(parent.Fd()), name, 0)
		return fmt.Errorf("retain created regular file")
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = unix.Unlinkat(int(parent.Fd()), name, 0)
		}
	}()
	if _, err := file.Write(contents); err != nil {
		return err
	}
	if err := file.Chmod(os.FileMode(mode)); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return syncDirectory(parent)
}

func requireAbsentAt(parent *os.File, name string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("entry already exists")
}

func openOrCreateParentAt(base *os.File, relative string, device uint64) (*os.File, string, error) {
	if err := validatePublicationRelativePath(relative); err != nil {
		return nil, "", err
	}
	parts := strings.Split(relative, "/")
	parent, err := duplicateDirectory(base, device)
	if err != nil {
		return nil, "", err
	}
	for _, component := range parts[:len(parts)-1] {
		next, _, openErr := ensureDirectoryAt(parent, component, 0o700, device)
		_ = parent.Close()
		if openErr != nil {
			return nil, "", openErr
		}
		parent = next
	}
	return parent, parts[len(parts)-1], nil
}

func openParentAt(base *os.File, relative string, device uint64) (*os.File, string, error) {
	if err := validatePublicationRelativePath(relative); err != nil {
		return nil, "", err
	}
	parts := strings.Split(relative, "/")
	parent, err := duplicateDirectory(base, device)
	if err != nil {
		return nil, "", err
	}
	for _, component := range parts[:len(parts)-1] {
		next, _, openErr := openDirectoryAt(parent, component, device)
		_ = parent.Close()
		if openErr != nil {
			return nil, "", openErr
		}
		parent = next
	}
	return parent, parts[len(parts)-1], nil
}

func ensureDirectoryAt(parent *os.File, name string, mode uint32, device uint64) (*os.File, bool, error) {
	created := false
	var initial unix.Stat_t
	err := unix.Fstatat(int(parent.Fd()), name, &initial, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		mkdirErr := unix.Mkdirat(int(parent.Fd()), name, mode)
		if mkdirErr == nil {
			created = true
		} else if !errors.Is(mkdirErr, unix.EEXIST) {
			return nil, false, mkdirErr
		}
		// A simultaneous first publisher may have won the exact mkdir race.
		// Fence the parent entry, then classify and retain it through the same
		// nofollow/same-device path below instead of reporting a spurious conflict.
		if err := syncDirectory(parent); err != nil {
			return nil, false, err
		}
	} else if err != nil {
		return nil, false, err
	} else if !statIsDirectory(&initial) {
		return nil, false, fmt.Errorf("entry is not a physical directory")
	}
	opened, _, err := openDirectoryAt(parent, name, device)
	if err == nil && created {
		if chmodErr := opened.Chmod(os.FileMode(mode)); chmodErr != nil {
			_ = opened.Close()
			return nil, created, chmodErr
		}
		if syncErr := opened.Sync(); syncErr != nil {
			_ = opened.Close()
			return nil, created, syncErr
		}
	}
	return opened, created, err
}

func openDirectoryAt(parent *os.File, name string, device uint64) (*os.File, unix.Stat_t, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return nil, unix.Stat_t{}, fmt.Errorf("invalid directory component")
	}
	var initial unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, unix.Stat_t{}, err
	}
	if !statIsDirectory(&initial) || uint64(initial.Dev) != device {
		return nil, unix.Stat_t{}, fmt.Errorf("entry is not a same-filesystem physical directory")
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, unix.Stat_t{}, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, unix.Stat_t{}, fmt.Errorf("retain directory")
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !statIsDirectory(&opened) || identityOf(&opened) != identityOf(&initial) {
		_ = file.Close()
		if err == nil {
			err = errors.New("directory identity changed")
		}
		return nil, unix.Stat_t{}, err
	}
	return file, opened, nil
}

func openOptionalDirectoryAt(parent *os.File, name string, device uint64) (*os.File, error) {
	opened, _, err := openDirectoryAt(parent, name, device)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	return opened, err
}

func duplicateDirectory(directory *os.File, device uint64) (*os.File, error) {
	fd, err := unix.Openat(int(directory.Fd()), ".", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), directory.Name())
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("duplicate directory")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || !statIsDirectory(&stat) || uint64(stat.Dev) != device {
		_ = file.Close()
		if err == nil {
			err = errors.New("duplicated directory changed filesystem")
		}
		return nil, err
	}
	return file, nil
}

func syncDirectory(directory *os.File) error {
	if directory == nil {
		return fmt.Errorf("sync nil directory")
	}
	return directory.Sync()
}

func retainedDirectoryIdentity(directory *os.File) (fileIdentity, error) {
	if directory == nil {
		return fileIdentity{}, fmt.Errorf("retain nil directory")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &stat); err != nil {
		return fileIdentity{}, err
	}
	if !statIsDirectory(&stat) {
		return fileIdentity{}, fmt.Errorf("retained entry is not a directory")
	}
	return identityOf(&stat), nil
}

func directoryBindingMatches(parent *os.File, name string, handle *os.File, expected fileIdentity) bool {
	if parent == nil || handle == nil {
		return false
	}
	var retained unix.Stat_t
	if err := unix.Fstat(int(handle.Fd()), &retained); err != nil || !statIsDirectory(&retained) || identityOf(&retained) != expected {
		return false
	}
	var current unix.Stat_t
	return unix.Fstatat(int(parent.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil &&
		statIsDirectory(&current) && identityOf(&current) == expected
}

func transactionCleanupRoster(transactionID string, journal *publicationJournal) (map[string]transactionCleanupEntry, error) {
	roster := map[string]transactionCleanupEntry{
		"stage":  {directory: true, mode: 0o700},
		"backup": {directory: true, mode: 0o700},
	}
	if journal == nil {
		return roster, nil
	}
	if journal.TransactionID != transactionID {
		return nil, fmt.Errorf("transaction cleanup journal id differs")
	}
	document, err := encodePublicationJournal(*journal)
	if err != nil {
		return nil, fmt.Errorf("transaction cleanup journal is invalid: %w", err)
	}
	roster["journal.tmp"] = transactionCleanupEntry{sha256: digestBytes(document), mode: 0o600}
	addDirectoryAncestors := func(relative string) {
		current := path.Dir(relative)
		for current != "." {
			roster[current] = transactionCleanupEntry{directory: true, mode: 0o700}
			current = path.Dir(current)
		}
	}
	for index, entry := range journal.Files {
		if entry.Next.Present {
			staged := path.Join("stage", entry.Path)
			addDirectoryAncestors(staged)
			roster[staged] = transactionCleanupEntry{sha256: entry.Next.SHA256, mode: entry.Next.Mode}
			if !fileStatesEqual(entry.Prior, entry.Next) {
				roster[path.Join("backup", publicationQuarantineName(index))] = transactionCleanupEntry{sha256: entry.Next.SHA256, mode: entry.Next.Mode}
			}
		}
		if entry.Prior.Present && entry.Prior.Owned && !fileStatesEqual(entry.Prior, entry.Next) {
			roster[path.Join("backup", publicationBackupName(index))] = transactionCleanupEntry{sha256: entry.Prior.SHA256, mode: entry.Prior.Mode}
		}
	}
	stagedManifest := path.Join("stage", generatedManifestRelativePath)
	addDirectoryAncestors(stagedManifest)
	roster[stagedManifest] = transactionCleanupEntry{sha256: journal.NextManifest.SHA256, mode: 0o644}
	if journal.PriorManifest.Present {
		roster[path.Join("backup", publicationBackupName(len(journal.Files)))] = transactionCleanupEntry{sha256: journal.PriorManifest.SHA256, mode: 0o644}
	}
	for index, directory := range journal.Directories {
		if directory.PriorPresent {
			continue
		}
		prepared := publicationDirectoryStageName(index)
		roster[prepared] = transactionCleanupEntry{directory: true, mode: 0o755}
		marker := path.Join(prepared, publicationDirectoryMarkerName(transactionID))
		roster[marker] = transactionCleanupEntry{
			sha256: digestBytes(publicationDirectoryMarkerContents(transactionID, directory.Path)),
			mode:   0o600,
		}
	}
	if len(roster) > maximumPublicationTransactionEntries {
		return nil, fmt.Errorf("transaction cleanup roster exceeds limit")
	}
	return roster, nil
}

func verifyStagedPublication(tx *publicationTransaction, bundle publicationBundle, device uint64) error {
	if tx == nil || tx.stage == nil {
		return fmt.Errorf("staged publication is unavailable")
	}
	roster := make(map[string]transactionCleanupEntry, len(bundle.files)*2+2)
	addAncestors := func(relative string) {
		current := path.Dir(relative)
		for current != "." {
			roster[current] = transactionCleanupEntry{directory: true, mode: 0o700}
			current = path.Dir(current)
		}
	}
	for _, file := range bundle.files {
		addAncestors(file.path)
		roster[file.path] = transactionCleanupEntry{sha256: file.sha256, mode: file.mode}
	}
	addAncestors(generatedManifestRelativePath)
	roster[generatedManifestRelativePath] = transactionCleanupEntry{sha256: bundle.manifestSHA, mode: 0o644}
	if len(roster) > maximumPublicationTransactionEntries {
		return fmt.Errorf("staged publication roster exceeds limit")
	}
	entriesSeen := 0
	return validateDirectoryContentsExact(tx.stage, device, "", roster, &entriesSeen)
}

func verifyStagedFile(tx *publicationTransaction, relative string, expected journalFileState, device uint64) error {
	if tx == nil || tx.stage == nil || !expected.Present {
		return fmt.Errorf("invalid staged file expectation")
	}
	parent, name, err := openParentAt(tx.stage, relative, device)
	if err != nil {
		return err
	}
	defer parent.Close()
	observed, _, err := observeRegularAt(parent, name, maximumPublicationFileBytes)
	if err != nil || !observed.present || observed.sha256 != expected.SHA256 || observed.mode != expected.Mode {
		return fmt.Errorf("staged file differs")
	}
	return nil
}

func verifyStagedManifest(tx *publicationTransaction, expected journalManifestState, device uint64) error {
	if tx == nil || tx.stage == nil || !expected.Present {
		return fmt.Errorf("invalid staged manifest expectation")
	}
	parent, name, err := openParentAt(tx.stage, generatedManifestRelativePath, device)
	if err != nil {
		return err
	}
	defer parent.Close()
	observed, _, err := observeRegularAt(parent, name, maximumPublicationJournalBytes)
	if err != nil || !observed.present || observed.sha256 != expected.SHA256 || observed.mode != 0o644 {
		return fmt.Errorf("staged manifest differs")
	}
	return nil
}

func removeDirectoryContentsExact(
	directory *os.File,
	device uint64,
	prefix string,
	roster map[string]transactionCleanupEntry,
	entriesSeen *int,
	afterRemove func(string) error,
) error {
	handle, err := duplicateDirectory(directory, device)
	if err != nil {
		return err
	}
	entries, readErr := handle.ReadDir(maximumPublicationTransactionEntries + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := handle.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if len(entries) > maximumPublicationTransactionEntries {
		return fmt.Errorf("transaction cleanup entry count exceeds limit")
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	for _, entry := range entries {
		*entriesSeen = *entriesSeen + 1
		if *entriesSeen > maximumPublicationTransactionEntries {
			return fmt.Errorf("transaction cleanup tree exceeds limit")
		}
		name := entry.Name()
		relative := name
		if prefix != "" {
			relative = path.Join(prefix, name)
		}
		expected, allowed := roster[relative]
		if !allowed {
			return fmt.Errorf("transaction cleanup has unexpected entry %s", relative)
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if statIsDirectory(&stat) {
			if !expected.directory {
				return fmt.Errorf("transaction cleanup entry %s has wrong kind", relative)
			}
			if uint32(stat.Mode)&0o7777 != expected.mode {
				return fmt.Errorf("transaction cleanup directory %s mode differs", relative)
			}
			if uint64(stat.Dev) != device {
				return fmt.Errorf("transaction directory crosses filesystem")
			}
			child, _, err := openDirectoryAt(directory, name, device)
			if err != nil {
				return err
			}
			if err := removeDirectoryContentsExact(child, device, relative, roster, entriesSeen, afterRemove); err != nil {
				_ = child.Close()
				return err
			}
			if err := child.Close(); err != nil {
				return err
			}
			if err := unix.Unlinkat(int(directory.Fd()), name, unix.AT_REMOVEDIR); err != nil {
				return err
			}
			if afterRemove != nil {
				if err := afterRemove(relative); err != nil {
					return err
				}
			}
			continue
		}
		if expected.directory || !statIsRegular(&stat) {
			return fmt.Errorf("transaction cleanup entry %s is not an expected regular file", relative)
		}
		observed, _, err := observeRegularAt(directory, name, maximumPublicationFileBytes)
		if err != nil || !observed.present || observed.sha256 != expected.sha256 || observed.mode != expected.mode {
			return fmt.Errorf("transaction cleanup entry %s differs from journal", relative)
		}
		if err := unix.Unlinkat(int(directory.Fd()), name, 0); err != nil {
			return err
		}
		if afterRemove != nil {
			if err := afterRemove(relative); err != nil {
				return err
			}
		}
	}
	return syncDirectory(directory)
}

func validateDirectoryContentsExact(
	directory *os.File,
	device uint64,
	prefix string,
	roster map[string]transactionCleanupEntry,
	entriesSeen *int,
) error {
	handle, err := duplicateDirectory(directory, device)
	if err != nil {
		return err
	}
	entries, readErr := handle.ReadDir(maximumPublicationTransactionEntries + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := handle.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if len(entries) > maximumPublicationTransactionEntries {
		return fmt.Errorf("transaction cleanup entry count exceeds limit")
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	for _, entry := range entries {
		*entriesSeen = *entriesSeen + 1
		if *entriesSeen > maximumPublicationTransactionEntries {
			return fmt.Errorf("transaction cleanup tree exceeds limit")
		}
		name := entry.Name()
		relative := name
		if prefix != "" {
			relative = path.Join(prefix, name)
		}
		expected, allowed := roster[relative]
		if !allowed {
			return fmt.Errorf("transaction cleanup has unexpected entry %s", relative)
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if statIsDirectory(&stat) {
			if !expected.directory || uint32(stat.Mode)&0o7777 != expected.mode || uint64(stat.Dev) != device {
				return fmt.Errorf("transaction cleanup directory %s differs", relative)
			}
			child, _, err := openDirectoryAt(directory, name, device)
			if err != nil {
				return err
			}
			validateErr := validateDirectoryContentsExact(child, device, relative, roster, entriesSeen)
			closeErr := child.Close()
			if validateErr != nil || closeErr != nil {
				return errors.Join(validateErr, closeErr)
			}
			continue
		}
		if expected.directory || !statIsRegular(&stat) {
			return fmt.Errorf("transaction cleanup entry %s is not an expected regular file", relative)
		}
		observed, _, err := observeRegularAt(directory, name, maximumPublicationFileBytes)
		if err != nil || !observed.present || observed.sha256 != expected.sha256 || observed.mode != expected.mode {
			return fmt.Errorf("transaction cleanup entry %s differs from journal", relative)
		}
	}
	return nil
}

func identityOf(stat *unix.Stat_t) fileIdentity {
	return fileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}
}

func statIsDirectory(stat *unix.Stat_t) bool {
	return uint32(stat.Mode)&unix.S_IFMT == unix.S_IFDIR
}

func statIsRegular(stat *unix.Stat_t) bool {
	return uint32(stat.Mode)&unix.S_IFMT == unix.S_IFREG
}
