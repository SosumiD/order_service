package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/order-service/internal/order"
	"example.com/order-service/internal/repository/memory"
	"example.com/order-service/internal/service"
)

func testRouter() http.Handler {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return NewRouter(service.NewOrderService(memory.NewOrderRepository()), logger)
}

func request(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
	return recorder
}

func expectStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("expected HTTP %d, got %d: %s", want, response.Code, response.Body.String())
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected JSON response, got %s", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing request id")
	}
}

func TestAPILifecycle(t *testing.T) {
	router := testRouter()
	health := request(router, http.MethodGet, "/health", "")
	expectStatus(t, health, http.StatusOK)
	if !strings.Contains(health.Body.String(), `"status":"ok"`) {
		t.Fatalf("wrong health response: %s", health.Body.String())
	}

	empty := request(router, http.MethodGet, "/api/v1/orders", "")
	expectStatus(t, empty, http.StatusOK)
	if strings.TrimSpace(empty.Body.String()) != "[]" {
		t.Fatalf("expected empty JSON array, got %s", empty.Body.String())
	}

	created := request(router, http.MethodPost, "/api/v1/orders", `{"items":[{"name":"Keyboard","quantity":2,"price":500000},{"name":"Mouse","quantity":1,"price":200000}]}`)
	expectStatus(t, created, http.StatusCreated)
	var value order.Order
	if err := json.Unmarshal(created.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.ID != 1 || value.TotalPrice != 1200000 || value.Status != order.StatusNew || value.CreatedAt.IsZero() {
		t.Fatalf("wrong created order: %+v", value)
	}

	list := request(router, http.MethodGet, "/api/v1/orders", "")
	expectStatus(t, list, http.StatusOK)
	var orders []order.Order
	if err := json.Unmarshal(list.Body.Bytes(), &orders); err != nil || len(orders) != 1 || orders[0].ID != value.ID {
		t.Fatalf("wrong list: %+v, %v", orders, err)
	}

	got := request(router, http.MethodGet, "/api/v1/orders/1", "")
	expectStatus(t, got, http.StatusOK)
	if err := json.Unmarshal(got.Body.Bytes(), &value); err != nil || value.ID != 1 {
		t.Fatalf("wrong get: %+v, %v", value, err)
	}

	for _, status := range []order.Status{order.StatusProcessing, order.StatusCompleted} {
		updated := request(router, http.MethodPatch, "/api/v1/orders/1/status", `{"status":"`+string(status)+`"}`)
		expectStatus(t, updated, http.StatusOK)
		if err := json.Unmarshal(updated.Body.Bytes(), &value); err != nil || value.Status != status {
			t.Fatalf("wrong update: %+v, %v", value, err)
		}
	}

	history := request(router, http.MethodGet, "/api/v1/orders/1/history", "")
	expectStatus(t, history, http.StatusOK)
	var changes []order.StatusChange
	if err := json.Unmarshal(history.Body.Bytes(), &changes); err != nil || len(changes) != 3 {
		t.Fatalf("wrong history: %+v, %v", changes, err)
	}
	for i, status := range []order.Status{order.StatusNew, order.StatusProcessing, order.StatusCompleted} {
		if changes[i].Status != status || (i > 0 && changes[i].ChangedAt.Before(changes[i-1].ChangedAt)) {
			t.Fatalf("wrong history sequence: %+v", changes)
		}
	}
}

func TestAPIValidation(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		code   int
		key    string
	}{
		{"malformed JSON", "POST", "/api/v1/orders", `{`, 400, "INVALID_JSON"},
		{"unknown field", "POST", "/api/v1/orders", `{"items":[],"unknown":1}`, 400, "INVALID_JSON"},
		{"extra JSON", "POST", "/api/v1/orders", `{"items":[]} {}`, 400, "INVALID_JSON"},
		{"empty body", "POST", "/api/v1/orders", ``, 400, "INVALID_JSON"},
		{"empty order", "POST", "/api/v1/orders", `{"items":[]}`, 400, "INVALID_ORDER"},
		{"blank name", "POST", "/api/v1/orders", `{"items":[{"name":" ","quantity":1,"price":1}]}`, 400, "INVALID_ORDER"},
		{"zero quantity", "POST", "/api/v1/orders", `{"items":[{"name":"A","quantity":0,"price":1}]}`, 400, "INVALID_ORDER"},
		{"negative price", "POST", "/api/v1/orders", `{"items":[{"name":"A","quantity":1,"price":-1}]}`, 400, "INVALID_ORDER"},
		{"invalid id", "GET", "/api/v1/orders/abc", ``, 400, "INVALID_ID"},
		{"zero id", "GET", "/api/v1/orders/0", ``, 400, "INVALID_ID"},
		{"missing order", "GET", "/api/v1/orders/999", ``, 404, "ORDER_NOT_FOUND"},
		{"missing history", "GET", "/api/v1/orders/999/history", ``, 404, "ORDER_NOT_FOUND"},
		{"invalid history id", "GET", "/api/v1/orders/bad/history", ``, 400, "INVALID_ID"},
		{"invalid status id", "PATCH", "/api/v1/orders/x/status", `{"status":"processing"}`, 400, "INVALID_ID"},
		{"malformed status", "PATCH", "/api/v1/orders/1/status", `{`, 400, "INVALID_JSON"},
		{"unknown status", "PATCH", "/api/v1/orders/1/status", `{"status":"invalid"}`, 400, "INVALID_STATUS"},
		{"missing status", "PATCH", "/api/v1/orders/1/status", `{}`, 400, "INVALID_STATUS"},
		{"status order missing", "PATCH", "/api/v1/orders/999/status", `{"status":"processing"}`, 404, "ORDER_NOT_FOUND"},
		{"forbidden transition", "PATCH", "/api/v1/orders/1/status", `{"status":"completed"}`, 409, "INVALID_STATUS_TRANSITION"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := testRouter()
			if tt.method == http.MethodPatch {
				created := request(router, http.MethodPost, "/api/v1/orders", `{"items":[{"name":"A","quantity":1,"price":1}]}`)
				expectStatus(t, created, http.StatusCreated)
			}
			response := request(router, tt.method, tt.path, tt.body)
			expectStatus(t, response, tt.code)
			var problem errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Code != tt.key || problem.Error == "" {
				t.Fatalf("wrong error: %+v, %v", problem, err)
			}
		})
	}
}

func TestAPIEndStates(t *testing.T) {
	for _, terminal := range []order.Status{order.StatusCompleted, order.StatusCancelled} {
		t.Run(string(terminal), func(t *testing.T) {
			router := testRouter()
			expectStatus(t, request(router, "POST", "/api/v1/orders", `{"items":[{"name":"A","quantity":1,"price":1}]}`), 201)
			if terminal == order.StatusCompleted {
				expectStatus(t, request(router, "PATCH", "/api/v1/orders/1/status", `{"status":"processing"}`), 200)
			}
			expectStatus(t, request(router, "PATCH", "/api/v1/orders/1/status", `{"status":"`+string(terminal)+`"}`), 200)
			response := request(router, "PATCH", "/api/v1/orders/1/status", `{"status":"cancelled"}`)
			expectStatus(t, response, 409)
		})
	}
}

func TestSwagger(t *testing.T) {
	router := testRouter()
	ui := request(router, "GET", "/swagger/", "")
	if ui.Code != http.StatusOK || !strings.Contains(ui.Body.String(), "SwaggerUIBundle") {
		t.Fatalf("Swagger UI not available: %d %s", ui.Code, ui.Body.String())
	}
	api := request(router, "GET", "/swagger/openapi.json", "")
	expectStatus(t, api, http.StatusOK)
	var spec struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.Body.Bytes(), &spec); err != nil || spec.OpenAPI == "" || len(spec.Paths) != 5 {
		t.Fatalf("invalid OpenAPI schema: %+v, %v", spec, err)
	}
	redirect := request(router, "GET", "/swagger", "")
	if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != "/swagger/" {
		t.Fatalf("unexpected redirect: %d %+v", redirect.Code, redirect.Header())
	}
}

type brokenService struct{ OrderService }

func (brokenService) List(context.Context) ([]order.Order, error) {
	return nil, errors.New("secret database connection details")
}

func TestAPIInternalError(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	router := NewRouter(brokenService{}, logger)
	response := request(router, "GET", "/api/v1/orders", "")
	expectStatus(t, response, 500)
	if strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), "INTERNAL_ERROR") {
		t.Fatalf("internal details exposed: %s", response.Body.String())
	}
}

func TestMiddleware(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := requestLoggingMiddleware(logger)(recoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panic" {
			panic("secret panic details")
		}
		writeJSON(w, http.StatusCreated, map[string]string{"ok": "yes"})
	})))
	for _, tt := range []struct {
		path   string
		status int
	}{
		{"/normal", 201},
		{"/panic", 500},
	} {
		response := request(server, "GET", tt.path, "")
		expectStatus(t, response, tt.status)
		if tt.status == 500 && strings.Contains(response.Body.String(), "secret") {
			t.Fatal("panic details exposed")
		}
	}
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 log records, got %d", len(records))
	}
	for _, record := range records {
		if record["msg"] == "http request" {
			if record["method"] != "GET" || record["path"] == "" || record["status"] == nil || record["duration"] == nil || record["request_id"] == "" {
				t.Fatalf("missing log fields: %+v", record)
			}
		}
	}
}
