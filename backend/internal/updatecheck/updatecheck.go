// Package updatecheck asks GitHub, at most once a week and only when an
// administrator has opted in, which release of Go Help Desk is the latest
// (#170).
//
// What leaves the server is one GET to the public releases API with a fixed
// User-Agent ("go-help-desk") and a constant Accept header. No version (issue
// #170: a version in the User-Agent turns "an instance exists here" into "an
// instance running 1.3.0 exists here"), no identifier, no query string, no
// cookie, no body. GitHub learns that an IP address fetched a file, once a
// week. Do not add anything to the request without changing the wording at
// setup, in the wizard and in Settings, and a test pins the exact headers.
//
// Air-gapped installs lose nothing: the setting is off unless chosen, and
// while it is off Check makes no request at all.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

const (
	// ReleasesURL is GitHub's "latest release" endpoint, which already leaves
	// out drafts and prereleases. Check ignores both again regardless.
	ReleasesURL = "https://api.github.com/repos/PubliciaLLC/go-help-desk/releases/latest"

	// releasePage is where a notice links. Built from the validated tag, never
	// from the response's own html_url, so a hostile response cannot choose
	// where an administrator is sent.
	releasePage = "https://github.com/PubliciaLLC/go-help-desk/releases/tag/"

	// Interval is the shortest time between two requests.
	Interval = 7 * 24 * time.Hour

	// DefaultTimeout bounds one request. A check is never worth a wait.
	DefaultTimeout = 10 * time.Second

	maxBody = 1 << 20
)

// Record is what the last check left behind.
type Record struct {
	// CheckedAt is when a request was last attempted, successful or not. A
	// failure waits for the next window like any other outcome, so an instance
	// with no egress does not retry on every tick.
	CheckedAt time.Time `json:"checked_at"`
	// Latest is the newest stable version seen, without a leading "v". Kept
	// across a failed check.
	Latest string `json:"latest,omitempty"`
}

// Load reads the stored record: the zero Record when no check has happened (or
// the row is damaged, which the next check overwrites) and an error when the
// settings could not be read. Callers must not treat that error as "never
// checked", which would turn a database outage into a request.
func Load(ctx context.Context, s *admin.Service) (Record, error) {
	all, err := s.ListAll(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("reading the update check record: %w", err)
	}
	var r Record
	if raw, ok := all[admin.KeyUpdateCheckResult]; !ok || json.Unmarshal(raw, &r) != nil {
		return Record{}, nil
	}
	return r, nil
}

// Available is what an administrator is told.
type Available struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	URL     string `json:"url"`
}

// Notice reports a newer release than current, from the stored record alone:
// rendering a page never causes a request. It is hidden while the check is
// off, so turning it off also removes an answer that would otherwise go stale.
func Notice(ctx context.Context, s *admin.Service, current string) (Available, bool) {
	if on, err := s.GetBool(ctx, admin.KeyUpdateCheckEnabled); err != nil || !on {
		return Available{}, false
	}
	rec, err := Load(ctx, s)
	if err != nil || rec.Latest == "" {
		return Available{}, false
	}
	// The stored text is not trusted either: it is rebuilt from numbers, and
	// the link from that, or there is no notice.
	latest, err := Canonical(rec.Latest)
	if err != nil || latest != rec.Latest {
		return Available{}, false
	}
	newer, err := Newer(current, latest)
	if err != nil || !newer {
		return Available{}, false
	}
	return Available{Current: current, Latest: latest, URL: releasePage + "v" + latest}, true
}

// Checker runs the weekly check.
type Checker struct {
	Settings *admin.Service
	Client   *http.Client
	URL      string
	Timeout  time.Duration
	Now      func() time.Time
	Log      *slog.Logger
}

// New is the Checker the server runs: the real API, the real clock.
func New(settings *admin.Service, log *slog.Logger) *Checker {
	return &Checker{
		Settings: settings,
		Client:   &http.Client{Timeout: DefaultTimeout},
		URL:      ReleasesURL,
		Now:      time.Now,
		Log:      log,
	}
}

// Run checks once now and then every hour, which only ever does anything when
// a week has passed. It returns when ctx ends.
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		c.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check makes a request if and only if the setting is on and no request has
// been made in the last Interval. It reports whether it did. A failure is one
// logged warning and is otherwise silent.
func (c *Checker) Check(ctx context.Context) bool {
	if on, err := c.Settings.GetBool(ctx, admin.KeyUpdateCheckEnabled); err != nil || !on {
		return false
	}
	rec, err := Load(ctx, c.Settings)
	if err != nil {
		// Not "never checked": that would make a database outage a request.
		c.Log.InfoContext(ctx, "skipping the update check; could not read when it last ran", "error", err)
		return false
	}
	now := c.Now()
	// A stored time in the future (a clock that was set back) does not block
	// the check for longer than a week.
	if since := now.Sub(rec.CheckedAt); since >= 0 && since < Interval {
		return false
	}

	// Claim the window BEFORE asking. Written after, a process killed during
	// the request (SIGTERM, a crash loop) would find no record and ask again on
	// every start. Detached from ctx for the same reason: the shutdown that
	// cancels it must not be what loses the claim.
	rec.CheckedAt = now
	c.save(ctx, rec)

	latest, err := c.fetch(ctx)
	switch {
	case err != nil:
		// INFO, not WARN (#170): a project site or GitHub being down is not the
		// operator's problem.
		c.Log.InfoContext(ctx, "checking for a new release failed; will try again next week", "error", err)
	case latest != "":
		rec.Latest = latest
		c.save(ctx, rec)
	}
	return true
}

func (c *Checker) save(ctx context.Context, rec Record) {
	raw, _ := json.Marshal(rec)
	if err := c.Settings.SetRaw(context.WithoutCancel(ctx), admin.KeyUpdateCheckResult, raw); err != nil {
		c.Log.InfoContext(ctx, "recording the update check failed", "error", err)
	}
}

// fetch returns the latest stable version, or "" for a response that is a
// draft or prerelease.
func (c *Checker) fetch(ctx context.Context) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "go-help-desk")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}

	var body struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		return "", fmt.Errorf("reading the release: %w", err)
	}
	if body.Draft || body.Prerelease {
		return "", nil
	}
	// Strict: a prerelease tag or anything with build metadata is an error here
	// (so, quietly, no answer), and what is returned is rebuilt from numbers.
	return Canonical(body.TagName)
}
