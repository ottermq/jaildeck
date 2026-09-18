package main

import (
	"net/http"
	"testing"
)

func TestNewServer_TimeoutsAreSet(t *testing.T) {
	srv := newServer("127.0.0.1:0", http.NewServeMux())

	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout is unset; it is what bounds slow-header clients")
	}
	if srv.ReadTimeout <= 0 {
		t.Error("ReadTimeout is unset")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout is unset")
	}
}

func TestNewServer_WriteTimeoutStaysUnset(t *testing.T) {
	srv := newServer("127.0.0.1:0", http.NewServeMux())

	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %s, want 0: it covers handler execution, and a "+
			"jail operation may run to the 150s backstop (JD-014)", srv.WriteTimeout)
	}
}
