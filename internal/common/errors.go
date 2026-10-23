package common

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

var (
	ErrForbidden         = NewAppError(http.StatusForbidden, "forbidden")
	ErrUnauthorized      = NewAppError(http.StatusUnauthorized, "unauthorized")
	ErrBadRequest        = NewAppError(http.StatusBadRequest, "invalid request")
	ErrInvalidAssigneeID = NewAppError(http.StatusBadRequest, "invalid assignee ID")
	ErrRequestTooLarge   = NewAppError(http.StatusRequestEntityTooLarge, "request body too large")
)

type AppError struct {
	Code    int
	Message string
}

func (e *AppError) Error() string { return e.Message }

func NewAppError(code int, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

// ErrorResponse is the body of every failed request. A named type rather than
// an inline map so the generated API spec can point at it; the JSON is
// identical either way.
type ErrorResponse struct {
	Error string `json:"error"`
}

func HandlerError(w http.ResponseWriter, err error) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		WriteJSON(w, appErr.Code, ErrorResponse{Error: appErr.Message})
		return
	}

	// The client went away mid-request. Nothing to report, and nobody left to
	// report it to. Mutations are detached from request cancellation (JD-014),
	// so the only thing that reaches here is a read whose caller disconnected.
	if errors.Is(err, context.Canceled) {
		return
	}

	slog.Error("unexpected error", slog.Any("err", err))

	WriteJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "internal server error",
	})
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode JSON response", slog.Any("err", err))
	}
}
