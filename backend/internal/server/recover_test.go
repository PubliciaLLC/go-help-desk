package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// quietLogs keeps the stack trace a panic logs out of the test output, and
// hands it back so a test can read it.
func quietLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// A panic answers the same JSON envelope every other server fault does, not an
// empty body that a client has to special-case (#421).
func TestJSONRecoverer_PanicAnswersTheErrorEnvelope(t *testing.T) {
	logs := quietLogs(t)
	h := jsonRecoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret internal detail")
	}))

	rr := httptest.NewRecorder()
	require.NotPanics(t, func() { h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil)) })

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var e errResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&e))
	require.Equal(t, "internal_error", e.Error.Code)
	require.NotContains(t, rr.Body.String(), "secret internal detail", "the panic value is for the log, not the client")
	require.Contains(t, logs.String(), "secret internal detail", "the panic must still be logged")
}

// http.ErrAbortHandler is how a handler says "drop this connection quietly".
// Swallowing it would turn that into a 500 nobody asked for.
func TestJSONRecoverer_DoesNotSwallowErrAbortHandler(t *testing.T) {
	quietLogs(t)
	h := jsonRecoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	rr := httptest.NewRecorder()
	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	})
	require.Empty(t, rr.Body.String())
}

// Once the status line has gone out there is no honest 500 left to send, and
// appending an envelope to a half-written body corrupts it.
func TestJSONRecoverer_WritesNothingAfterTheResponseStarted(t *testing.T) {
	quietLogs(t)
	h := jsonRecoverer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("late")
	}))
	rr := httptest.NewRecorder()
	require.NotPanics(t, func() { h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil)) })
	require.Equal(t, http.StatusAccepted, rr.Code)
	require.Equal(t, "partial", rr.Body.String())
}

// The wrapper must not hide http.Flusher: mcp-go's SSE transport asserts it.
func TestJSONRecoverer_KeepsFlusher(t *testing.T) {
	var flusher bool
	h := jsonRecoverer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, flusher = w.(http.Flusher)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", strings.NewReader("")))
	require.True(t, flusher)
}
