package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/open-help-desk/open-help-desk/backend/internal/database/ticketstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/userstore"
)

// JSON writes v as JSON with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errResponse is the standard error envelope returned by the API.
type errResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Error writes a JSON error response.
func Error(w http.ResponseWriter, status int, code, message string) {
	var body errResponse
	body.Error.Code = code
	body.Error.Message = message
	JSON(w, status, body)
}

// DecodeJSON reads and decodes JSON from r.Body into dst.
func DecodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// handleError maps common sentinel errors to HTTP status codes.
func handleError(w http.ResponseWriter, err error) {
	if errors.Is(err, userstore.ErrNotFound) || errors.Is(err, ticketstore.ErrNotFound) {
		Error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	slog.Error("internal error", "error", err)
	Error(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
}
