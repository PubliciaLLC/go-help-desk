package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// reputationRequestBudget is how long ONE HTTP request may spend, in total,
// asking third parties about attachments.
//
// A whole-request figure and not a per-lookup one, which is the distinction
// the per-client 15s timeout in internal/reputation/client.go cannot make. The
// lookups run in series — every enabled provider, for every quarantined
// attachment on the ticket — and an outbound firewall that drops packets makes
// each of them cost the full timeout rather than failing fast. Four providers
// and three files is twelve of them, against an http.Server whose WriteTimeout
// is 30s (cmd/server/main.go): the handler is still running when the server
// gives up on the response, so staff do not get a slow attachment list, they
// get one that never loads, on every render, until the provider recovers.
//
// Five seconds because a healthy hash lookup is a couple of hundred
// milliseconds and nothing legitimate needs a second of that; the rest of the
// thirty stays with the database work and the write. A request that spends it
// all renders "unavailable" beside every provider, which is the honest answer
// and one the page already knows how to draw.
const reputationRequestBudget = 5 * time.Second

// repDeadlineKey carries that budget as a wall-clock instant, stamped once per
// request.
//
// An instant rather than a duration, and shared rather than re-derived, is the
// whole mechanism: every provider and every attachment in one request measures
// itself against the same point in time, so the total is the budget no matter
// how the work divides up.
type repDeadlineKey struct{}

// reputationDeadline stamps each request with the instant after which it will
// start no further reputation lookup.
//
// Middleware rather than something the attachment handlers do for themselves,
// because the bound has to cover a request and only the request knows where it
// began. It is installed for every route: the cost is one context value, and
// a handler that grows a lookup later inherits the bound rather than having to
// remember it.
func reputationDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), repDeadlineKey{},
			time.Now().Add(reputationRequestBudget))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// reputationDeadlineFrom reads that stamp.
//
// A context with no stamp gets a fresh budget of its own rather than none: the
// direct callers are tests and anything that later looks a hash up outside an
// HTTP request, and the failure to fail safely towards is "no bound at all".
// What such a caller does NOT get is a bound shared with its siblings, because
// there is nothing to share it through — which is exactly why the middleware
// exists.
func reputationDeadlineFrom(ctx context.Context) time.Time {
	if t, ok := ctx.Value(repDeadlineKey{}).(time.Time); ok {
		return t
	}
	return time.Now().Add(reputationRequestBudget)
}

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
//
// Nor can it hang. Every service it builds carries the request's shared
// deadline (see reputationRequestBudget), so a provider that accepts
// connections and never answers costs this request that budget once, however
// many attachments and providers there are, rather than once per pair. What an
// exhausted budget produces is the "unavailable" entry above — a rendered row,
// never an error and never a half-written response.
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
			//
			// And the same for a lookup the request's deadline left no room
			// for. One provider hung and that is the WARN worth reading; every
			// other provider and every other attachment on the ticket was then
			// skipped without a request going out, and those are the line the
			// first one would drown in.
			level := slog.LevelWarn
			if errors.Is(err, reputation.ErrRateLimited) ||
				errors.Is(err, reputation.ErrQuotaExceeded) ||
				errors.Is(err, reputation.ErrDeadlinePassed) {
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
	// One instant for every service built for this request, and
	// reputationServices is called once per attachment with the same context,
	// so every attachment on the ticket shares it too. That is what bounds the
	// request rather than the lookup.
	deadline := reputationDeadlineFrom(ctx)

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
		svc.Deadline = deadline
		out = append(out, svc)
	}
	return out
}
