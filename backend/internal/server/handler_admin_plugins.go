package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Admin plugin registry management.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── Plugins ──────────────────────────────────────────────────────────────────

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, s.plugins.List())
}

func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	Error(w, http.StatusNotImplemented, "not_implemented", "WASM plugin upload not yet implemented")
}

func (s *Server) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	var err error
	if body.Enabled {
		err = s.plugins.Enable(id)
	} else {
		err = s.plugins.Disable(id)
	}
	if err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	Error(w, http.StatusNotImplemented, "not_implemented", "plugin uninstall not yet implemented")
}
