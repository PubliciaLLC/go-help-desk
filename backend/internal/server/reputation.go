package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// addReputation fills in what the configured reputation service says about a
// quarantined attachment, looking it up once if nobody has yet.
//
// Lazy, on a page render, because this project has no background job runner
// (#126): anything queued at upload time would be a table nothing drains. The
// cost that makes that safe is one lookup per hash per refresh interval — a
// fortnight by default, never for a detection — and a budget in front of it
// for the rest.
//
// It cannot fail. Every outcome other than a completed lookup leaves
// att.Reputation nil, which renders as "not checked yet": no key, no cache, a
// spent budget, a provider that is down, a reader who closed the tab. An
// attachment list that 500s because somebody else's free tier ran out is a
// worse help desk than one that shows a file without a verdict.
func (s *Server) addReputation(ctx context.Context, att *ticket.Attachment) {
	// A hash is what is looked up, and quarantine is what makes it worth
	// looking up. Every attachment carries a hash now, and asking about all of
	// them would spend a 500-a-day allowance on holiday-request PDFs.
	if att.SHA256 == nil || *att.SHA256 == "" || att.VirusName == nil {
		return
	}

	svc := s.reputationLookup(ctx)
	if svc == nil {
		return
	}

	rep, err := svc.GetOrLookup(ctx, *att.SHA256)
	if err != nil {
		// The operator is the audience: they configured a key and a provider,
		// and a lookup that never completes is theirs to fix. Nothing here
		// carries the key — see reputationLookup.
		//
		// Except when the budget refused it, which is not an incident and
		// clears on its own — within the minute, or at 00:00 UTC. At WARN
		// that is a line per quarantined attachment per page view for the
		// rest of the day, and the lines that do matter drown in it. The
		// comment above scanUpload is about precisely that failure: a warning
		// nobody reads is not a warning.
		level := slog.LevelWarn
		if errors.Is(err, reputation.ErrRateLimited) || errors.Is(err, reputation.ErrQuotaExceeded) {
			level = slog.LevelDebug
		}
		slog.Log(ctx, level, "attachment reputation lookup did not complete",
			"error", err, "provider", s.adminSvc.ReputationProvider(ctx))
		// Deliberately no return: GetOrLookup reports a verdict it could not
		// cache, and a stale verdict it could not re-check, as a verdict plus
		// an error — and those verdicts are worth showing. Every other error
		// arrives with State Unavailable, which the switch below drops.
	}

	att.Reputation = s.attachmentReputation(ctx, rep)
}

// attachmentReputation is one verdict as the API sends it, or nil when there
// is nothing honest to send.
//
// Shared by the page render and the manual re-check so that the two cannot
// describe the same verdict differently — the difference that would matter
// being the fetch time, which decides whether the control the re-check lives
// on is armed.
func (s *Server) attachmentReputation(ctx context.Context, rep reputation.Reputation) *ticket.AttachmentReputation {
	out := &ticket.AttachmentReputation{
		State:      string(rep.State),
		ThreatName: rep.ThreatName,
		AnalysedAt: rep.AnalysedAt,
		Provider:   reputation.DisplayName(s.adminSvc.ReputationProvider(ctx)),
	}

	// When we last asked. A verdict read from the cache carries the row's
	// fetched_at; one fetched by the call that just returned it carries none,
	// because a provider has no idea when we asked — and that is now, to
	// within the length of an HTTP request. Sending null there would leave the
	// "check again" control looking armed a second after a lookup.
	fetched := rep.FetchedAt
	if fetched.IsZero() {
		fetched = time.Now()
	}
	out.FetchedAt = &fetched

	switch rep.State {
	case reputation.Detected, reputation.Clean:
		detected, total := rep.Detected, rep.Total
		out.Detected, out.Total = &detected, &total
	case reputation.Unseen, reputation.Unscanned:
		// No analysis, so no numbers: the pointers stay nil rather than
		// carrying 0, because "0 of 0 engines" reads as a clean result and
		// this is the opposite of one.
	default:
		// Unavailable, and the zero State a future provider might return.
		// Both mean nobody has an answer, and the wire says that by carrying
		// nothing at all.
		return nil
	}
	return out
}

// reputationLookup builds the get-or-lookup service for this request, or
// returns nil when the feature is off.
//
// The split here is the one thing in this file worth reading twice. The
// provider and the key are read per call, because both are settings an
// operator can change while the server runs and a value captured in New would
// leave their change doing nothing until a restart — the defect this codebase
// already documents for the ticket prefix and the scanner address. The Budget
// is the opposite: it holds the counters, so it must be the same instance
// every time. Built per call it would hand every request a fresh allowance,
// and the cap would mean nothing at all.
//
// Constructing the rest is a handful of struct fields and no I/O, so there is
// nothing to cache and no invalidation to get wrong.
//
// nil is returned for both halves of "off": no store to cache verdicts in, and
// no API key. The key IS the on switch — there is no separate enabled flag, so
// there is no such thing as enabled-with-no-key and no invalid combination for
// an operator to land in.
func (s *Server) reputationLookup(ctx context.Context) *reputation.Service {
	if s.repStore == nil {
		return nil
	}
	// Never logged, never wrapped into an error, never returned to a client.
	// It is stored write-only, and this is the only place it is read.
	key := s.adminSvc.ReputationAPIKey(ctx)
	if key == "" {
		return nil
	}
	svc := reputation.NewService(s.newReputationProvider(ctx, key), s.repStore, s.repBudget)
	// Read per call for the same reason as the provider and the key: an
	// operator who changes how often verdicts are re-checked should not have
	// to restart the server to mean it.
	svc.RefreshAfter = s.adminSvc.ReputationRefreshInterval(ctx)
	return svc
}
