package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"example.com/order-service/docs"
	"example.com/order-service/internal/order"
)

type OrderService interface {
	Create(ctx context.Context, input order.CreateOrderInput) (order.Order, error)
	GetByID(ctx context.Context, id int64) (order.Order, error)
	List(ctx context.Context) ([]order.Order, error)
	UpdateStatus(ctx context.Context, id int64, status order.Status) (order.Order, error)
	History(ctx context.Context, id int64) ([]order.StatusChange, error)
}

type Handler struct {
	service OrderService
	logger  *slog.Logger
}

func NewRouter(service OrderService, logger *slog.Logger) http.Handler {
	h := &Handler{service: service, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("POST /api/v1/orders", h.create)
	mux.HandleFunc("GET /api/v1/orders", h.list)
	mux.HandleFunc("GET /api/v1/orders/{id}", h.get)
	mux.HandleFunc("PATCH /api/v1/orders/{id}/status", h.updateStatus)
	mux.HandleFunc("GET /api/v1/orders/{id}/history", h.history)
	mux.Handle("GET /swagger/", http.StripPrefix("/swagger/", http.FileServer(http.FS(docs.Files))))
	mux.HandleFunc("GET /swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/", http.StatusMovedPermanently)
	})
	return requestLoggingMiddleware(logger)(recoveryMiddleware(logger)(mux))
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var input order.CreateOrderInput
	if !readJSON(w, r, &input) {
		return
	}
	value, err := h.service.Create(r.Context(), input)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	values, err := h.service.List(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, values)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	value, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	var input order.UpdateStatusInput
	if !readJSON(w, r, &input) {
		return
	}
	value, err := h.service.UpdateStatus(r.Context(), id, input.Status)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	values, err := h.service.History(r.Context(), id)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, values)
}

func readID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid order ID", Code: "INVALID_ID"})
		return 0, false
	}
	return id, true
}

func readJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON", Code: "INVALID_JSON"})
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON", Code: "INVALID_JSON"})
		return false
	}
	return true
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, order.ErrInvalidOrder):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "INVALID_ORDER"})
	case errors.Is(err, order.ErrInvalidStatus):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid status", Code: "INVALID_STATUS"})
	case errors.Is(err, order.ErrOrderNotFound):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "order not found", Code: "ORDER_NOT_FOUND"})
	case errors.Is(err, order.ErrInvalidStatusTransition):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "status transition is not allowed", Code: "INVALID_STATUS_TRANSITION"})
	default:
		h.logger.Error("request failed", slog.Any("error", err))
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error", Code: "INTERNAL_ERROR"})
	}
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
