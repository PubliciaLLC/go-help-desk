// Package antivirus scans uploaded files, and — the part that matters — is
// able to say that it could not.
//
// The scanner this replaces returned (infected bool, virusName string). There
// was no way to express "I could not reach the daemon", so every failure —
// unconfigured, unreachable, a write error mid-stream — collapsed into
// infected=false, which every caller read as clean. An instance whose ClamAV
// container had been dead for a month accepted everything, logged a warning
// nobody reads, and told the administrator nothing.
//
// A scanner that cannot distinguish "clean" from "I don't know" is not a
// control. It is a thing that sometimes catches a virus.
package antivirus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Verdict is what the scanner concluded. The third case is the point of the
// package.
type Verdict string

const (
	// Clean means the file was scanned and nothing was found. Only ever
	// returned after a scan actually completed.
	Clean Verdict = "clean"

	// Infected means the scanner identified something. Virus names it.
	Infected Verdict = "infected"

	// Unavailable means no scan happened: no scanner configured, unreachable,
	// or the conversation failed part-way. Never conflated with Clean.
	Unavailable Verdict = "unavailable"
)

// Result is a scan outcome. Reason is set only for Unavailable and is for
// operators, not for callers to parse.
type Result struct {
	Verdict Verdict
	Virus   string
	Reason  error
}

// Policy decides what an Unavailable verdict means for an upload.
type Policy string

const (
	// PolicyOff does not scan at all. Honest about it: the admin UI says so,
	// rather than an operator inferring it from an unset variable.
	PolicyOff Policy = "off"

	// PolicyRequired refuses what it cannot scan. The default wherever a
	// scanner address is configured, because configuring a scanner and then
	// accepting unscanned files is not a position anyone holds deliberately.
	PolicyRequired Policy = "required"

	// PolicyPermissive accepts what it cannot scan — the old behaviour, named
	// so that choosing it is a choice rather than a default nobody saw.
	PolicyPermissive Policy = "permissive"
)

// ValidPolicy reports whether s names a policy, so a settings write can be
// refused rather than silently falling back.
func ValidPolicy(s string) bool {
	switch Policy(s) {
	case PolicyOff, PolicyRequired, PolicyPermissive:
		return true
	}
	return false
}

// ErrNotConfigured is the reason when no scanner address is set.
var ErrNotConfigured = errors.New("no scanner is configured")

// Scanner talks to a clamd daemon over INSTREAM.
//
// Safe for concurrent use: it holds no connection between calls, dialling per
// scan. clamd forks a worker per connection, so this is the shape it expects.
type Scanner struct {
	addr        string
	dialTimeout time.Duration
	scanTimeout time.Duration
}

// New returns a Scanner for addr, which may be "tcp://host:port" or
// "unix:///path/to/socket". An empty addr yields a Scanner whose every scan is
// Unavailable with ErrNotConfigured — deliberately not a nil Scanner, so a
// caller cannot forget to check and get a nil dereference instead of a policy
// decision.
func New(addr string) *Scanner {
	return &Scanner{
		addr:        addr,
		dialTimeout: 5 * time.Second,
		// Generous: a 25 MB file over INSTREAM on a busy daemon is not fast,
		// and a timeout that fires on a healthy scanner would produce
		// Unavailable, which under PolicyRequired refuses a legitimate upload.
		scanTimeout: 60 * time.Second,
	}
}

// Configured reports whether an address was supplied at all.
func (s *Scanner) Configured() bool { return s.addr != "" }

// Ping asks the daemon whether it is alive, without sending a file.
//
// Used to show scanner state in the admin UI: an operator should be able to
// see that scanning is degraded without reading logs, and without waiting for
// an upload to fail to find out.
func (s *Scanner) Ping(ctx context.Context) error {
	if !s.Configured() {
		return ErrNotConfigured
	}
	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(s.dialTimeout)); err != nil {
		return err
	}
	if _, err := conn.Write([]byte("zPING\x00")); err != nil {
		return fmt.Errorf("pinging scanner: %w", err)
	}
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("reading ping response: %w", err)
	}
	if !strings.HasPrefix(string(buf[:n]), "PONG") {
		return fmt.Errorf("scanner answered %q, not PONG", strings.TrimRight(string(buf[:n]), "\x00\n"))
	}
	return nil
}

// Scan streams data to clamd and reports what it found, or that it could not
// look.
//
// Every error path returns Unavailable. None returns Clean. That is the whole
// contract: Clean is reachable only by a completed scan that found nothing.
func (s *Scanner) Scan(ctx context.Context, data []byte) Result {
	if !s.Configured() {
		return Result{Verdict: Unavailable, Reason: ErrNotConfigured}
	}
	conn, err := s.dial(ctx)
	if err != nil {
		return unavailable("dialling scanner", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(s.scanTimeout)); err != nil {
		return unavailable("setting deadline", err)
	}

	// INSTREAM: zINSTREAM\0, then [4-byte big-endian length][chunk]…,
	// terminated by a zero length.
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return unavailable("starting stream", err)
	}
	const chunkSize = 4096
	for i := 0; i < len(data); i += chunkSize {
		end := min(i+chunkSize, len(data))
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(end-i))
		if _, err := conn.Write(size[:]); err != nil {
			return unavailable("writing chunk length", err)
		}
		if _, err := conn.Write(data[i:end]); err != nil {
			return unavailable("writing chunk", err)
		}
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return unavailable("ending stream", err)
	}

	// Read the single-line reply rather than to EOF: clamd answers and then
	// closes, but a half-open connection would otherwise block until the
	// deadline.
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return unavailable("reading verdict", err)
	}
	reply := strings.TrimRight(string(buf[:n]), "\x00\n")

	switch {
	case strings.HasSuffix(reply, "FOUND"):
		// "stream: Eicar-Test-Signature FOUND"
		fields := strings.Fields(reply)
		name := "unknown"
		if len(fields) >= 2 {
			name = fields[len(fields)-2]
		}
		return Result{Verdict: Infected, Virus: name}
	case strings.HasSuffix(reply, "OK"):
		return Result{Verdict: Clean}
	default:
		// An ERROR reply, or anything unrecognised. Not clean: a reply we
		// cannot parse is a scan we cannot vouch for.
		return Result{Verdict: Unavailable, Reason: fmt.Errorf("scanner replied %q", reply)}
	}
}

func (s *Scanner) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: s.dialTimeout}
	if strings.HasPrefix(s.addr, "unix://") {
		return d.DialContext(ctx, "unix", strings.TrimPrefix(s.addr, "unix://"))
	}
	return d.DialContext(ctx, "tcp", strings.TrimPrefix(s.addr, "tcp://"))
}

func unavailable(doing string, err error) Result {
	return Result{Verdict: Unavailable, Reason: fmt.Errorf("%s: %w", doing, err)}
}
