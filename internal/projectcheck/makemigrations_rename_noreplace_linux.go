//go:build linux

package projectcheck

import "golang.org/x/sys/unix"

func makemigrationsRenameNoReplace(oldDirectory int, oldName string, newDirectory int, newName string) error {
	return unix.Renameat2(oldDirectory, oldName, newDirectory, newName, unix.RENAME_NOREPLACE)
}
