package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

var nextRequestID atomic.Uint64

func requestLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			id := strconv.FormatUint(nextRequestID.Add(1), 10)
			w.Header().Set("X-Request-ID", id)
			writer := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(writer, r)
			status := writer.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.Info("http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", id),
			)
		})
	}
}

func recoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if value := recover(); value != nil {
					logger.Error("panic recovered", slog.Any("error", value), slog.String("path", r.URL.Path))
					writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error", Code: "INTERNAL_ERROR"})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
