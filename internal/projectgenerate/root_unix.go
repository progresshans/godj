//go:build darwin || linux

package projectgenerate

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// SealProjectRoot binds one already-selected physical project path to the
// expected device/inode identity. It performs no project mutation and retains
// no descriptor that a caller must close.
func SealProjectRoot(projectRoot string, expectedDevice, expectedInode uint64) (ProjectRoot, error) {
	if expectedDevice == 0 || expectedInode == 0 {
		return ProjectRoot{}, fmt.Errorf("seal project root: expected identity is empty")
	}
	root, err := captureProjectRoot(projectRoot)
	if err != nil {
		return ProjectRoot{}, err
	}
	if root.device != expectedDevice || root.inode != expectedInode {
		return ProjectRoot{}, fmt.Errorf("seal project root: %w: selected directory identity changed", ErrGeneratedConflict)
	}
	return root, nil
}

func captureProjectRoot(projectRoot string) (ProjectRoot, error) {
	absolute, err := canonicalProjectRoot(projectRoot)
	if err != nil {
		return ProjectRoot{}, fmt.Errorf("seal project root: %w: %v", ErrGeneratedConflict, err)
	}
	directory, err := openPhysicalProjectRoot(absolute)
	if err != nil {
		return ProjectRoot{}, fmt.Errorf("seal project root: %w", err)
	}
	defer directory.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &stat); err != nil || !statIsDirectory(&stat) {
		if err == nil {
			err = fmt.Errorf("path is not a physical directory")
		}
		return ProjectRoot{}, fmt.Errorf("seal project root: %w: %v", ErrGeneratedConflict, err)
	}
	return ProjectRoot{absolute: absolute, device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}

func resolveProjectRoot(root ProjectRoot) (ProjectRoot, error) {
	if root.absolute == "" {
		return ProjectRoot{}, fmt.Errorf("project root seal is empty")
	}
	if root.device == 0 || root.inode == 0 {
		return captureProjectRoot(root.absolute)
	}
	current, err := captureProjectRoot(root.absolute)
	if err != nil {
		return ProjectRoot{}, err
	}
	if current.device != root.device || current.inode != root.inode {
		return ProjectRoot{}, fmt.Errorf("project root seal: %w: directory identity changed", ErrGeneratedConflict)
	}
	return root, nil
}

func verifyProjectRoot(root ProjectRoot) error {
	_, err := resolveProjectRoot(root)
	return err
}
