//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/progresshans/godj/project/firstaccount"
)

// One explicitly submitted path is opened once. Nonblocking acquisition lets
// us reject FIFOs/devices/directories without consuming them. The same regular
// file handle supplies metadata and bounded bytes; no pathname is sent to the
// child. The operation owns a detached byte snapshot, not a reusable local path.
func readFirstAccountFile(ctx context.Context, interrupt <-chan struct{}, directory string, path []byte, maximum int) (upload *firstaccount.Upload, code string) {
	if len(path) == 0 || maximum < 0 || maximum > firstaccount.MaxUploadBytes {
		return nil, "invalid_input"
	}
	if code = commandInterruption(ctx, interrupt); code != "" {
		return nil, code
	}
	name := filepath.Base(string(path))
	if !firstaccount.ValidUploadName(name) {
		return nil, "invalid_input"
	}
	selected := string(path)
	if !filepath.IsAbs(selected) {
		selected = filepath.Join(directory, selected)
	}
	file, err := os.OpenFile(selected, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, "input_read_failed"
	}
	var content []byte
	defer func() {
		if file.Close() != nil {
			code = "input_read_failed"
		}
		if code != "" {
			clear(content)
			upload = nil
		}
	}()
	before, err := file.Stat()
	if err != nil {
		return nil, "input_read_failed"
	}
	if !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > int64(maximum) {
		return nil, "invalid_input"
	}
	content = make([]byte, int(before.Size())+1)
	used := 0
	for {
		if code = commandInterruption(ctx, interrupt); code != "" {
			return nil, code
		}
		n, err := file.Read(content[used:min(len(content), used+32768)])
		used += n
		if used > int(before.Size()) {
			return nil, "input_read_failed"
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "input_read_failed"
		}
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || used != int(before.Size()) {
		return nil, "input_read_failed"
	}
	if code = commandInterruption(ctx, interrupt); code != "" {
		return nil, code
	}
	return &firstaccount.Upload{Name: name, Content: content[:used]}, ""
}
