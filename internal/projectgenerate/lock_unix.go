//go:build darwin || linux

package projectgenerate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type publicationLock struct {
	file *os.File
	root *publicationRoot
}

func acquirePublicationLock(ctx context.Context, root *publicationRoot) (*publicationLock, error) {
	if ctx == nil {
		return nil, fmt.Errorf("acquire publication lock: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := filepath.Base(publicationLockRelativePath)
	var fd int
	var initial unix.Stat_t
	created := false
	for {
		err := unix.Fstatat(int(root.godj.Fd()), name, &initial, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			fd, err = unix.Openat(
				int(root.godj.Fd()),
				name,
				unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK,
				0o600,
			)
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("acquire publication lock: %w", err)
			}
			created = true
			break
		}
		if err != nil {
			return nil, fmt.Errorf("acquire publication lock: %w", err)
		}
		if !statIsRegular(&initial) {
			return nil, fmt.Errorf("acquire publication lock: lock is not a regular file")
		}
		fd, err = unix.Openat(
			int(root.godj.Fd()),
			name,
			unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK,
			0,
		)
		if err != nil {
			return nil, fmt.Errorf("acquire publication lock: %w", err)
		}
		break
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("acquire publication lock: retain file")
	}
	failed := func(err error) (*publicationLock, error) {
		_ = file.Close()
		return nil, err
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !statIsRegular(&opened) || (!created && identityOf(&opened) != identityOf(&initial)) {
		if err == nil {
			err = errors.New("lock is not a regular file")
		}
		return failed(fmt.Errorf("acquire publication lock: %w", err))
	}
	if created {
		if err := errors.Join(file.Chmod(0o600), file.Sync(), syncDirectory(root.godj)); err != nil {
			return failed(fmt.Errorf("acquire publication lock: persist lock: %w", err))
		}
	}
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return failed(fmt.Errorf("acquire publication lock: %w", err))
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return failed(ctx.Err())
		case <-timer.C:
		}
	}
	var current unix.Stat_t
	if err := unix.Fstatat(int(root.godj.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil || !statIsRegular(&current) || identityOf(&current) != identityOf(&opened) {
		_ = unix.Flock(fd, unix.LOCK_UN)
		if err == nil {
			err = errors.New("lock identity changed")
		}
		return failed(fmt.Errorf("acquire publication lock: %w", err))
	}
	root.lockIdentity = identityOf(&opened)
	root.lockHeld = true
	return &publicationLock{file: file, root: root}, nil
}

func (lock *publicationLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	if lock.root != nil {
		lock.root.lockHeld = false
		lock.root.lockIdentity = fileIdentity{}
	}
	err := errors.Join(
		unix.Flock(int(lock.file.Fd()), unix.LOCK_UN),
		lock.file.Close(),
	)
	lock.file = nil
	lock.root = nil
	return err
}
