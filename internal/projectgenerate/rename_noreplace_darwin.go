//go:build darwin

package projectgenerate

import "golang.org/x/sys/unix"

func renameNoReplace(oldDirectory int, oldName string, newDirectory int, newName string) error {
	return unix.RenameatxNp(oldDirectory, oldName, newDirectory, newName, unix.RENAME_EXCL)
}
