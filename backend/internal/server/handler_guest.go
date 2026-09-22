package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// The guest surface. Four routes, each doing one thing, mounted outside the
// ticket router.
//
// Deliberately not achieved by relaxing RequireRole on /tickets. Doing that
// would put all nineteen routes under /tickets/{id} in the position of each
// re-deriving whether the caller is a guest and what that permits — which is
// the shape of the BFLA fixed in 1.2.0, where the check lived in two handlers
// and not in the seventeen beneath them. Here /tickets keeps one total rule:
// you are signed in, or you are not there.

// guestTicketFromRequest returns the ticket the request's token named.
//
// The id comes from the middleware, never from the URL or the body, so there
// is no parameter for a guest to change.
func (s *Server) guestTicketFromRequest(r *http.Request) (ticket.Ticket, bool) {
	idStr, ok := authmw.GuestTicketID(r)
	if !ok {
		return ticket.Ticket{}, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return ticket.Ticket{}, false
	}
	t, err := s.tickets.GetByID(r.Context(), id)
	if err != nil {
		return ticket.Ticket{}, false
	}
	return t, true
}

// POST /api/v1/guest/tickets
//
// 404 when guest submission is off, not 403: an instance that does not offer
// this should not advertise that it could.
func (s *Server) handleGuestCreateTicket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.adminSvc.GuestSubmissionEnabled(ctx) {
		Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	// Keyed on the source address, like signup, which is the only key there is
	// when there is no account to key on.
	if !s.loginLimiter.Allow("guest-submit:" + authmw.ClientAddr(r)) {
		tooManyAttempts(w, time.Minute)
		return
	}

	var body struct {
		Subject     string     `json:"subject"`
		Description string     `json:"description"`
		CategoryID  uuid.UUID  `json:"category_id"`
		TypeID      *uuid.UUID `json:"type_id"`
		ItemID      *uuid.UUID `json:"item_id"`
		GuestEmail  string     `json:"guest_email"`
		GuestName   string     `json:"guest_name"`
		GuestPhone  string     `json:"guest_phone"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	email := strings.TrimSpace(body.GuestEmail)
	if email == "" {
		Error(w, http.StatusBadRequest, "bad_request", "guest_email is required")
		return
	}
	// Priority is not taken from the request. A guest setting their own ticket
	// to critical is a queue anyone on the internet can jump.
	in := ticket.CreateInput{
		Subject:        body.Subject,
		Description:    body.Description,
		CategoryID:     body.CategoryID,
		TypeID:         body.TypeID,
		ItemID:         body.ItemID,
		Priority:       ticket.PriorityMedium,
		GuestEmail:     &email,
		GuestName:      strings.TrimSpace(body.GuestName),
		GuestPhone:     strings.TrimSpace(body.GuestPhone),
		TrackingPrefix: s.adminSvc.TicketPrefix(ctx),
	}
	t, err := s.tickets.Create(ctx, in)
	if err != nil {
		if errors.Is(err, ticket.ErrValidation) {
			Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		handleError(w, err)
		return
	}

	// The tracking number and nothing else. Not the ticket id, not the token:
	// the token goes by email, so possession of the mailbox is the credential.
	// Answering with it here would hand access to whoever sent the request,
	// which is not necessarily whoever owns the address.
	JSON(w, http.StatusCreated, map[string]string{
		"tracking_number": string(t.TrackingNumber),
	})
}

// GET /api/v1/guest/ticket
func (s *Server) handleGuestGetTicket(w http.ResponseWriter, r *http.Request) {
	t, ok := s.guestTicketFromRequest(r)
	if !ok {
		Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	replies, err := s.tickets.ListReplies(r.Context(), t.ID)
	if err != nil {
		handleError(w, err)
		return
	}
	statuses, err := s.tickets.ListStatuses(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	statusName := ""
	for _, st := range statuses {
		if st.ID == t.StatusID {
			statusName = st.Name
			break
		}
	}

	// A deliberately narrow view, built here rather than returning the ticket.
	// Marshalling ticket.Ticket would ship assignee ids, group ids, the
	// reporter id and the resolution notes to someone with no account, and
	// would keep doing so as fields are added.
	JSON(w, http.StatusOK, map[string]any{
		"tracking_number": t.TrackingNumber,
		"subject":         t.Subject,
		"description":     t.Description,
		"status":          statusName,
		"created_at":      t.CreatedAt,
		"updated_at":      t.UpdatedAt,
		"replies":         guestReplyView(ticket.VisibleReplies(replies, user.RoleUser)),
	})
}

// guestReplyView strips a reply to what a guest may see. RoleUser has already
// removed internal notes; this removes the author id, which names a member of
// staff to someone outside the organisation.
func guestReplyView(replies []ticket.Reply) []map[string]any {
	out := make([]map[string]any, 0, len(replies))
	for _, r := range replies {
		out = append(out, map[string]any{
			"id":         r.ID,
			"body":       r.Body,
			"created_at": r.CreatedAt,
			// A reply with no author is the guest's own: nothing else writes
			// one. That is all a guest needs to tell the two apart, and it
			// names nobody.
			"from_you": r.AuthorID == nil,
		})
	}
	return out
}

// POST /api/v1/guest/replies
func (s *Server) handleGuestAddReply(w http.ResponseWriter, r *http.Request) {
	t, ok := s.guestTicketFromRequest(r)
	if !ok {
		Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		Error(w, http.StatusBadRequest, "bad_request", "body is required")
		return
	}

	ctx := r.Context()
	reply, err := s.tickets.AddGuestReply(ctx, t.ID, body.Body,
		s.adminSvc.ReopenWindowDays(ctx), s.reopenTargetStatusID(ctx))
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, map[string]any{
		"id":         reply.ID,
		"body":       reply.Body,
		"created_at": reply.CreatedAt,
		"from_you":   true,
	})
}

// POST /api/v1/guest/resend
//
// Always 202, whether or not anything matched. A response that varied would
// turn this into a way to test whether a tracking number or an address exists.
func (s *Server) handleGuestResend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.adminSvc.GuestSubmissionEnabled(ctx) {
		Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !s.loginLimiter.Allow("guest-resend:" + authmw.ClientAddr(r)) {
		tooManyAttempts(w, time.Minute)
		return
	}
	var body struct {
		TrackingNumber string `json:"tracking_number"`
		Email          string `json:"email"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	// Everything below is best-effort and its outcome is never reported.
	s.resendGuestLink(r, strings.TrimSpace(body.TrackingNumber), strings.TrimSpace(body.Email))

	w.WriteHeader(http.StatusAccepted)
}

// resendGuestLink mails a fresh link when the two halves match a ticket that
// still accepts access. Failures are swallowed on purpose: the caller learns
// nothing either way.
func (s *Server) resendGuestLink(r *http.Request, trackingNumber, email string) {
	if trackingNumber == "" || email == "" {
		return
	}
	ctx := r.Context()
	ticketID, err := s.tickets.GuestTicketIDFor(ctx, ticket.TrackingNumber(trackingNumber), email)
	if err != nil {
		return
	}
	// Minting and mailing both happen in the service, which already holds the
	// dispatcher. Whatever it returns is discarded: the caller answered 202
	// before this ran and must not say anything different now.
	_ = s.tickets.ResendGuestLink(ctx, ticketID)
}
