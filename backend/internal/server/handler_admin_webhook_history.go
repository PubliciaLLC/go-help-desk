package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// WebhookRedeliverer queues an earlier delivery's event to its hook again.
// notify.WebhookDispatcher is the implementation.
type WebhookRedeliverer interface {
	Redeliver(ctx context.Context, hookID, deliveryID uuid.UUID) error
}

// WithWebhookRedeliverer enables the Redeliver action. Without it the
// endpoint answers 503, so a deployment that never wired one fails loudly
// rather than accepting a request it will not act on.
func WithWebhookRedeliverer(r WebhookRedeliverer) Option {
	return func(s *Server) { s.webhookRedeliverer = r }
}

// handleListWebhookDeliveries returns a hook's last deliveries (#448): the
// outcome of each and nothing else. The stored event, the URL and the secret
// are not in the response, and neither is any error text.
func (s *Server) handleListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if _, err := s.authStore.GetWebhook(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	list, err := s.authStore.ListWebhookDeliveries(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	if list == nil {
		list = []authstore.WebhookDeliveryRecord{}
	}
	JSON(w, http.StatusOK, list)
}

// handleRedeliverWebhook queues the stored event of one delivery again, as a
// new delivery. 202: it is sent by the worker, not on this request.
func (s *Server) handleRedeliverWebhook(w http.ResponseWriter, r *http.Request) {
	hookID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	deliveryID, err := uuid.Parse(chi.URLParam(r, "delivery"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid delivery ID")
		return
	}
	if s.webhookRedeliverer == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "redelivery is not available")
		return
	}
	switch err := s.webhookRedeliverer.Redeliver(r.Context(), hookID, deliveryID); {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, notify.ErrWebhookNotFound), errors.Is(err, notify.ErrDeliveryNotFound):
		Error(w, http.StatusNotFound, "not_found", "no such webhook delivery")
	case errors.Is(err, notify.ErrWebhookDisabled):
		Error(w, http.StatusConflict, "webhook_disabled", "this webhook is disabled; enable it to redeliver")
	case errors.Is(err, notify.ErrWebhookNotSubscribed):
		Error(w, http.StatusConflict, "webhook_not_subscribed",
			"this webhook is no longer subscribed to that event; add it to the webhook's events to redeliver")
	case errors.Is(err, notify.ErrWebhookFilterExcludes):
		Error(w, http.StatusConflict, "webhook_filter_excludes",
			"this webhook's ticket filter no longer admits that ticket; change the filter to redeliver")
	case errors.Is(err, notify.ErrWebhookFilterUnverifiable):
		Error(w, http.StatusConflict, "webhook_filter_unverifiable",
			"the ticket no longer exists, so the filter can't be checked")
	default:
		handleError(w, err)
	}
}
