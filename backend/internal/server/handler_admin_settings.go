package server

import (
	"encoding/json"
	"net/http"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// Admin instance settings, and the denylist of keys never returned to a client.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── Settings ─────────────────────────────────────────────────────────────────

// secretSettingKeys are write-only over the API: they are accepted by
// PATCH /admin/settings but never returned by the settings dump. The dedicated
// endpoints blank them for the same reason (see handleGetOIDCConfig).
var secretSettingKeys = map[string]struct{}{
	admin.KeyOIDCClientSecret: {},
	admin.KeySAMLKeyPEM:       {},
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.adminSvc.ListAll(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	// Convert raw bytes to JSON-parseable map, omitting secrets.
	out := make(map[string]json.RawMessage, len(all))
	for k, v := range all {
		if _, secret := secretSettingKeys[k]; secret {
			continue
		}
		out[k] = json.RawMessage(v)
	}
	JSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	for k, v := range body {
		if err := s.adminSvc.SetRaw(r.Context(), k, []byte(v)); err != nil {
			handleError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
