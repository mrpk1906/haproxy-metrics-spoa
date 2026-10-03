package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMockBackendHandlers(t *testing.T) {
	mux := setupRouter()

	t.Run("GET /", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if rec.Body.String() != "mock-backend-ok\n" {
			t.Fatalf("expected body 'mock-backend-ok\\n', got %q", rec.Body.String())
		}
	})

	t.Run("POST /echo", func(t *testing.T) {
		payload := []byte("hello payload")
		req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), payload) {
			t.Fatalf("expected echoed body %q, got %q", string(payload), rec.Body.String())
		}
	})

	t.Run("GET /status/404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/404", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})

	t.Run("GET /status/500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/500", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", rec.Code)
		}
	})

	t.Run("GET /status/abc", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/abc", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("GET /status/99", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/99", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("GET /status/600", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/600", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("GET /nonexistent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})

	t.Run("GET /delay/50", func(t *testing.T) {
		start := time.Now()
		req := httptest.NewRequest(http.MethodGet, "/delay/50", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		elapsed := time.Since(start)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if elapsed < 40*time.Millisecond {
			t.Fatalf("expected delay >= 40ms, got %v", elapsed)
		}
	})

	t.Run("GET /healthz", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})
}
