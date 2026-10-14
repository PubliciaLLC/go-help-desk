package server

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// jsonRecoverer turns a handler panic into the standard 500 envelope. chi's
// Recoverer answers 500 with an empty body, which every client then has to
// special-case next to the JSON the rest of the API returns (#421).
//
// The panic value and stack go to the log with the request id and nowhere
// near the client: a panic message can carry anything the handler held.
func jsonRecoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &startedWriter{ResponseWriter: w}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// The server's own way to abort a response; it is not a fault, and
			// swallowing it would leave the connection open on a torn body.
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			slog.Error("panic in handler",
				"request_id", chimw.GetReqID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"panic", rec,
				"stack", string(debug.Stack()),
			)
			// After the status line has gone, any envelope would be appended to
			// a half-written body.
			if !sw.started {
				Error(sw, http.StatusInternalServerError, "internal_error", "an internal error occurred")
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// startedWriter remembers whether the response has begun.
type startedWriter struct {
	http.ResponseWriter
	started bool
}

func (sw *startedWriter) WriteHeader(code int) {
	sw.started = true
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *startedWriter) Write(b []byte) (int, error) {
	sw.started = true
	return sw.ResponseWriter.Write(b)
}

// Flush is forwarded for the reason statusRecorder's is: mcp-go's SSE
// transport asserts http.Flusher and answers 500 when it is missing.
func (sw *startedWriter) Flush() {
	sw.started = true
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (sw *startedWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }
