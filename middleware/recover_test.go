package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoverReturns500(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := Recover(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	if w.Body.String() != `{"error":"internal server error"}` {
		t.Errorf("unexpected body %q", w.Body.String())
	}
}

func TestRecoverReraisesAbort(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := Recover(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))

	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Errorf("expected ErrAbortHandler to be raised again, got %v", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
