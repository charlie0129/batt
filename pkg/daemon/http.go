package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/charlie0129/batt/pkg/logging"
)

// writeJSON answers with v as indented JSON, the format the CLI and GUI
// clients have always read.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		slog.Error("failed to encode response", "error", err)
		b = []byte(`"failed to encode response"`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// fail answers with err's message as a JSON string and records err for the
// access log.
func (d *Daemon) fail(w http.ResponseWriter, status int, err error) {
	if rw, ok := w.(*responseWriter); ok {
		rw.err = err
	}
	writeJSON(w, status, err.Error())
}

// decodeJSON reads the request body into v.
func decodeJSON(r *http.Request, v any) error {
	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return errors.New("request body is empty")
	}
	return err
}

// responseWriter records what a handler answered, for the access log.
type responseWriter struct {
	http.ResponseWriter
	status int
	size   int
	err    error
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.size += n
	return n, err
}

// Flush lets the event stream push frames through the wrapper.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

// accessLog logs one line per request: at trace level for successes, warn
// for client errors and error for server errors.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r)
		latency := time.Since(start).Milliseconds()

		msg := fmt.Sprintf("%s %s %d (%dms)", r.Method, r.URL.Path, rw.status, latency)
		attrs := []any{
			"statusCode", rw.status,
			"latency", latency, // time to process
			"method", r.Method,
			"path", r.URL.Path,
			"dataLength", rw.size,
		}
		if rw.err != nil {
			attrs = append(attrs, "error", rw.err)
		}
		switch {
		case rw.status >= http.StatusInternalServerError:
			slog.Error(msg, attrs...)
		case rw.status >= http.StatusBadRequest:
			slog.Warn(msg, attrs...)
		default:
			logging.Trace(msg, attrs...)
		}
	})
}

// recoverPanics turns a panicking handler into a 500 instead of a dead
// daemon.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(p)
			}
			slog.Error("request handler panicked", "panic", p, "method", r.Method, "path", r.URL.Path, "stack", string(debug.Stack()))
			writeJSON(w, http.StatusInternalServerError, fmt.Sprintf("internal error: %v", p))
		}()
		next.ServeHTTP(w, r)
	})
}
