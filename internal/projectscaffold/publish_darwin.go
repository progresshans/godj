//go:build darwin

package projectscaffold

import "golang.org/x/sys/unix"

func renameProjectNoReplace(parent int, oldName, newName string) error {
	return unix.RenameatxNp(parent, oldName, parent, newName, unix.RENAME_EXCL)
}
