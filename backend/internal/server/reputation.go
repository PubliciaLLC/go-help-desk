package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// providerVerdict is one enabled provider's answer on its way to the wire.
//
// The provider name travels WITH the verdict rather than beside it in a
// parallel slice, because the two getting out of step is a verdict rendered
// under another service's name — which is the mislabelling the cache's
// composite key already exists to prevent at the other end.
type providerVerdict struct {
	provider string
	rep      reputation.Reputation
}

// addReputation asks every enabled reputation service what it knows about a
// quarantined attachment, looking each one up once if nobody has yet.
//
// Lazy, on a page render, because this project has no background job runner
// (#126): anything queued at upload time would be a table nothing drains. The
// cost that makes that safe is one lookup per hash PER PROVIDER per refresh
// interval — a fortnight by default, never for a detection — and a per-
// provider budget in front of each.
//
// It cannot fail. With no provider enabled att.Reputation stays nil and the
// attachment renders exactly as it does today, which is a complete answer and
// not a degraded one: the local scanner decided this file's fate on its own.
// A provider that IS enabled and could not be asked leaves an "unavailable"
// entry instead, because an operator whose key has been rejected has to be
// able to see that.
func (s *Server) addReputation(ctx context.Context, att *ticket.Attachment) {
	// A hash is what is looked up, and quarantine is what makes it worth
	// looking up. Every attachment carries a hash now, and asking about all of
	// them would spend a 500-a-day allowance on holiday-request PDFs.
	if att.SHA256 == nil || *att.SHA256 == "" || att.VirusName == nil {
		return
	}

	svcs := s.reputationServices(ctx)
	if len(svcs) == 0 {
		// Nothing is enabled, or nothing enabled can run. Nothing was
		// attempted, so nothing is said — and specifically not "not checked
		// yet", which describes an attempt that did not finish.
		return
	}

	verdicts := make([]providerVerdict, 0, len(svcs))
	for _, svc := range svcs {
		rep, err := svc.GetOrLookup(ctx, *att.SHA256)
		if err != nil {
			// The operator is the audience: they enabled a provider and gave
			// it a key, and a lookup that never completes is theirs to fix.
			// Nothing here carries the key — see reputationServices.
			//
			// Except when the budget refused it, which is not an incident and
			// clears on its own — within the minute, or at 00:00 UTC. At WARN
			// that is a line per provider per quarantined attachment per page
			// view for the rest of the day, and the lines that do matter
			// drown in it.
			level := slog.LevelWarn
			if errors.Is(err, reputation.ErrRateLimited) || errors.Is(err, reputation.ErrQuotaExceeded) {
				level = slog.LevelDebug
			}
			slog.Log(ctx, level, "attachment reputation lookup did not complete",
				"error", err, "provider", svc.Provider())
			// Deliberately no continue: GetOrLookup reports a verdict it could
			// not cache, and a stale verdict it could not re-check, as a
			// verdict plus an error — and those verdicts are worth showing.
			// Everything else arrives as Unavailable, which is its own entry.
		}
		verdicts = append(verdicts, providerVerdict{provider: svc.Provider(), rep: rep})
	}

	att.Reputation = s.attachmentReputation(*att.SHA256, verdicts)
}

// attachmentReputation turns one verdict per enabled provider into the payload
// the API sends: every answer, plus the worst of them for the row.
//
// Shared by the page render and the manual re-check so that the two cannot
// describe the same verdicts differently — the difference that would matter
// being the fetch time, which decides whether the Check again control is
// armed.
//
// nil when nothing was attempted, which is how "no provider is enabled"
// reaches the wire. It is never nil merely because a lookup failed: that is an
// "unavailable" entry, because an operator whose key has been rejected has to
// be able to see it.
func (s *Server) attachmentReputation(sha256 string, verdicts []providerVerdict) *ticket.AttachmentReputation {
	if len(verdicts) == 0 {
		return nil
	}
	now := time.Now()

	states := make([]reputation.State, len(verdicts))
	lines := make([]ticket.AttachmentProviderVerdict, len(verdicts))
	for i, v := range verdicts {
		states[i] = v.rep.State
		lines[i] = s.providerLine(sha256, v, now)
	}
	out := &ticket.AttachmentReputation{Providers: lines}

	worst := reputation.Worst(states)
	if worst < 0 {
		// Nothing answered. "unavailable" is the honest summary of a row whose
		// every provider is failing, and the block stays on the wire rather
		// than disappearing, because four dead providers is exactly the thing
		// an operator has to be shown. The summary is attributed to nobody,
		// because nobody said it.
		out.State = string(reputation.Unavailable)
		return out
	}
	lines[worst].Inline = true

	// The summary is a COPY of one line rather than an aggregate of all of
	// them, and that is what keeps it honest. 62 of 81 engines is a fact about
	// VirusTotal's analysis; combined with a catalogue hit from CIRCL it would
	// become a number no service ever said. ProviderKey and the Inline flag
	// say which line this is, so the row's single sentence can be traced to
	// the service that said it.
	w := lines[worst]
	out.State = w.State
	out.Detected, out.Total = w.Detected, w.Total
	out.ThreatName = w.ThreatName
	out.KnownFeeds = w.KnownFeeds
	out.AnalysedAt = w.AnalysedAt
	out.FetchedAt = w.FetchedAt
	out.Provider, out.ProviderKey = w.Provider, w.ProviderKey
	return out
}

// providerLine is one provider's answer as the expanded view shows it.
func (s *Server) providerLine(sha256 string, v providerVerdict, now time.Time) ticket.AttachmentProviderVerdict {
	rep := v.rep
	line := ticket.AttachmentProviderVerdict{
		Provider:    reputation.DisplayName(v.provider),
		ProviderKey: v.provider,
		State:       string(rep.State),
		ThreatName:  rep.ThreatName,
		AnalysedAt:  rep.AnalysedAt,
		// Empty on every state but "known", where they are the evidence behind
		// the only reassuring verdict this feature produces — and where they
		// are what says how much that reassurance is worth.
		KnownFeeds: rep.KnownFeeds,
	}
	if rep.State == "" {
		// The zero State, which a future provider could return and which has
		// no branch anywhere downstream. Rendered as what it actually is: an
		// answer nobody gave.
		line.State = string(reputation.Unavailable)
	}

	if rep.State == reputation.Detected || rep.State == reputation.Clean {
		// Only a completed analysis has numbers. Everywhere else the pointers
		// stay nil rather than carrying 0, because "0 of 0 engines" reads as a
		// clean result — including for "known", where the file was answered
		// out of a catalogue and never scanned at all, so a count would be a
		// fabricated analysis attached to the one verdict staff are entitled
		// to find reassuring.
		detected, total := rep.Detected, rep.Total
		line.Detected, line.Total = &detected, &total
	}

	// When we last asked. A verdict read from the cache carries the row's
	// fetched_at; one fetched by the call that just returned it carries none,
	// because a provider has no idea when we asked — and that is now, to
	// within the length of an HTTP request. Sending null there would leave the
	// Check again control looking armed a second after a lookup.
	fetched := rep.FetchedAt
	if fetched.IsZero() {
		fetched = now
	}
	line.FetchedAt = &fetched
	line.Recheckable = reputation.Recheckable(rep.State, fetched, now)

	// This provider's own page for the hash, beside its own verdict. Empty for
	// a provider with no per-hash web UI, and the field is then omitted rather
	// than pointed at a page that cannot answer the question the reader
	// clicked it with. No key is passed: a link needs none.
	if url := s.newReputationProvider(v.provider, "").LinkURL(sha256); url != "" {
		line.LinkURL = &url
	}
	return line
}

// reputationServices builds one get-or-lookup service per enabled provider, in
// canonical order.
//
// The toggles and the keys are read per call, because both are settings an
// operator can change while the server runs and a value captured in New would
// leave their change doing nothing until a restart — the defect this codebase
// already documents for the ticket prefix and the scanner address. The Budget
// is the opposite: it holds the counters, so it must be the same instance
// every time, and it is deliberately shared across every service built here.
// Its counters are per provider, which is what keeps an exhausted VirusTotal
// allowance from stopping CIRCL answering.
//
// Constructing the rest is a handful of struct fields and no I/O, so there is
// nothing to cache and no invalidation to get wrong.
//
// An empty slice is returned when there is nowhere to cache verdicts, when no
// provider is enabled, and — per provider — when an enabled one cannot run.
// That last case is unreachable through the settings endpoint, which refuses
// to enable a commercial provider without a key; it fails closed here rather
// than sending an unauthenticated request to a service the operator has an
// account with.
func (s *Server) reputationServices(ctx context.Context) []*reputation.Service {
	if s.repStore == nil {
		return nil
	}
	enabled := s.adminSvc.EnabledReputationProviders(ctx)
	if len(enabled) == 0 {
		return nil
	}
	// Read per call for the same reason as the toggles: an operator who
	// changes how often verdicts are re-checked should not have to restart the
	// server to mean it.
	refreshAfter := s.adminSvc.ReputationRefreshInterval(ctx)

	out := make([]*reputation.Service, 0, len(enabled))
	for _, name := range enabled {
		// Never logged, never wrapped into an error, never returned to a
		// client. There are three of these now, and this is the only place any
		// of them is read.
		key := s.adminSvc.ReputationKey(ctx, name)
		if !reputation.CanLookup(name, key) {
			continue
		}
		svc := reputation.NewService(s.newReputationProvider(name, key), s.repStore, s.repBudget)
		svc.RefreshAfter = refreshAfter
		out = append(out, svc)
	}
	return out
}
