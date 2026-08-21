//go:build linux

package projectgenerate

import "golang.org/x/sys/unix"

func renameNoReplace(oldDirectory int, oldName string, newDirectory int, newName string) error {
	return unix.Renameat2(oldDirectory, oldName, newDirectory, newName, unix.RENAME_NOREPLACE)
}
