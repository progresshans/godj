//go:build darwin

package projectcheck

import "golang.org/x/sys/unix"

func makemigrationsRenameNoReplace(oldDirectory int, oldName string, newDirectory int, newName string) error {
	return unix.RenameatxNp(oldDirectory, oldName, newDirectory, newName, unix.RENAME_EXCL)
}
