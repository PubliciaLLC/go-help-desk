package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// CheckAttachmentDir reports why dir cannot take a new attachment, or nil.
//
// It makes the directory and its tickets subdirectory if absent and writes then
// removes a scratch file in each, which is what an upload does, so a pass means
// an upload can store its file (#458). Used at startup for the log line and on every upload attempt and
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
	// Probe the root and ATTACHMENT_DIR/tickets, where an upload writes. The
	// root alone passed when tickets was unusable and then every upload failed.
	// The root stays in because a read-only root has always meant "refuse
	// uploads", whether or not tickets happens to be writable.
	for _, d := range []string{dir, filepath.Join(dir, attachSubdir)} {
		if err := probeWrite(d); err != nil {
			return err
		}
	}
	return nil
}

// probeWrite makes d if absent, then writes a byte into a scratch file there
// and removes it.
func probeWrite(d string) error {
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(d, ".write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	// A byte written and synced, not an empty file: a full disk or an exhausted
	// quota lets the file be created and refuses the data.
	_, werr := f.Write([]byte{0})
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	rerr := os.Remove(name)
	return errors.Join(werr, cerr, rerr)
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
