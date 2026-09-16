package common

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestHandlerError_ClientDisconnectIsQuiet(t *testing.T) {
	logs := captureLog(t)
	rec := httptest.NewRecorder()

	HandlerError(rec, fmt.Errorf("listing jails: %w", context.Canceled))

	if logs.Len() != 0 {
		t.Errorf("logged %q, want nothing", logs.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("wrote %q, want no body", rec.Body.String())
	}
}

func TestHandlerError_UnexpectedErrorStillLogsAnd500s(t *testing.T) {
	logs := captureLog(t)
	rec := httptest.NewRecorder()

	HandlerError(rec, errors.New("boom"))

	if !strings.Contains(logs.String(), "unexpected error") {
		t.Errorf("log = %q, want it to report the error", logs.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Code = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandlerError_AppErrorKeepsItsStatus(t *testing.T) {
	logs := captureLog(t)
	rec := httptest.NewRecorder()

	HandlerError(rec, NewAppError(http.StatusNotFound, "jail not found"))

	if logs.Len() != 0 {
		t.Errorf("logged %q, want nothing", logs.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
