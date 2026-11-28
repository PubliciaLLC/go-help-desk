package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/crewjam/saml"
)

// samlAssertionTimeout bounds the ledger write. HandleAssertion is handed no
// context by the library, so without this a stalled database would hang the
// ACS request for as long as the client stays connected.
const samlAssertionTimeout = 5 * time.Second

// samlAssertionSpender makes a SAML assertion single-use (#618).
//
// The library validates the response and the request tracking cookie, but
// remembers nothing: a captured response body plus the browser's tracking
// cookie replays until the assertion's NotOnOrAfter (about 90 seconds with
// the library's clock skew allowance), minting a fresh hand-over token each
// time. It is installed as the middleware's AssertionHandler, which the
// library calls after full validation and before it creates a session.
//
// It records through the same ledger as the hand-over cookie
// (SpendSAMLHandover, spent_saml_handovers), under a prefix that cannot collide
// with a cookie's header.payload input: that input is base64url and dots, and
// this one starts with a word and a NUL. The ledger is shared between
// replicas, which an in-process cache would not be. Any error answers as an
// invalid assertion, so a database outage fails closed, as /complete already does.
//
// An IdP that reuses assertion IDs breaks the SAML specification and will be
// refused from the second sign-in on.
type samlAssertionSpender struct {
	spend func(ctx context.Context, key string) (first bool, err error)
}

var errSAMLAssertionReplayed = errors.New("SAML assertion already used or not spendable")

func (h samlAssertionSpender) HandleAssertion(a *saml.Assertion) error {
	// An empty ID cannot be told apart from another empty ID, so it could not
	// be made single-use. The schema requires one; refuse what lacks it.
	if a == nil || a.ID == "" {
		return errSAMLAssertionReplayed
	}
	// Detached from any request: the library gives us none, and once the row is
	// written the outcome must not depend on the client staying connected.
	ctx, cancel := context.WithTimeout(context.Background(), samlAssertionTimeout)
	defer cancel()
	first, err := h.spend(ctx, "saml-assertion\x00"+a.Issuer.Value+"\x00"+a.ID)
	if err != nil {
		slog.Error("recording a SAML assertion failed; refusing it", "error", err)
		return errSAMLAssertionReplayed
	}
	if !first {
		slog.Warn("a SAML assertion was presented again; refusing it")
		return errSAMLAssertionReplayed
	}
	return nil
}
