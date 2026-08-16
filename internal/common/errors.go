package common

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

var (
	ErrInvalidOwnerID     = NewAppError(http.StatusBadRequest, "invalid owner ID")
	ErrInvalidUserID      = NewAppError(http.StatusBadRequest, "invalid user ID")
	ErrInvalidWorkspaceID = NewAppError(http.StatusBadRequest, "invalid workspace ID")
	ErrInvalidProjectID   = NewAppError(http.StatusBadRequest, "invalid project ID")
	ErrForbidden          = NewAppError(http.StatusForbidden, "forbidden")
	ErrUnauthorized       = NewAppError(http.StatusUnauthorized, "unauthorized")
	ErrBadRequest         = NewAppError(http.StatusBadRequest, "invalid request")
	ErrInvalidAssigneeID  = NewAppError(http.StatusBadRequest, "invalid assignee ID")
)

type AppError struct {
	Code    int
	Message string
}

func (e *AppError) Error() string { return e.Message }

func NewAppError(code int, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

func HandlerError(w http.ResponseWriter, err error) {
	var appErr *AppError

	if errors.As(err, &appErr) {
		WriteJSON(w, appErr.Code, map[string]string{
			"error": appErr.Message,
		})
		return
	}
	log.Printf("unexpected error: %v", err)

	WriteJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "internal server error",
	})
}

func WriteJSON(w http.ResponseWriter, staus int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(staus)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("failed to encode JSON response: %v", err)
	}
}
