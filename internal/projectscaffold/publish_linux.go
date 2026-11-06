//go:build linux

package projectscaffold

import "golang.org/x/sys/unix"

func renameProjectNoReplace(parent int, oldName, newName string) error {
	return unix.Renameat2(parent, oldName, parent, newName, unix.RENAME_NOREPLACE)
}
