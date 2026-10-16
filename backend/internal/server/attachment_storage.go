package server

import (
	"errors"
	"io/fs"
	"os"
)

// CheckAttachmentDir reports why dir cannot take a new attachment, or nil.
//
// It makes the directory if absent and writes then removes a scratch file,
// which is what an upload does, so a pass means an upload can store its file
// (#458). Used at startup for the log line and on every upload attempt and
// every security-warnings read, rather than cached: the probe costs one tiny
// file, and it means fixing the permissions recovers at once with no restart
// and no timer to get wrong.
func CheckAttachmentDir(dir string) error {
	// Refused outright: os.CreateTemp("") writes to the system temp directory
	// and would pass, while uploads go to a path relative to the working
	// directory.
	if dir == "" {
		return errors.New("ATTACHMENT_DIR is empty")
	}
	f, err := os.CreateTemp(dir, ".write-check-*")
	if errors.Is(err, fs.ErrNotExist) {
		if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
			return mkErr
		}
		f, err = os.CreateTemp(dir, ".write-check-*")
	}
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// attachmentStorageProblem is CheckAttachmentDir's failure as a short reason
// that is safe to show anyone: the operating system's words ("permission
// denied") without the path, which belongs to the server.
func attachmentStorageProblem(dir string) string {
	err := CheckAttachmentDir(dir)
	if err == nil {
		return ""
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}
