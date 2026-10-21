package main

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/state"
)

func runToken(subArgs []string, out, errOut io.Writer, stateDir string) error {
	if len(subArgs) < 1 {
		return errors.New("expected 'add', 'list' or 'revoke' command")
	}
	switch subArgs[0] {
	case "add":
		if len(subArgs[1:]) < 1 {
			return errors.New("usage: jaildeck token add <name>")
		}
		if len(subArgs[1:]) > 1 {
			return ErrUnexpectedArgs
		}
		name := subArgs[1]
		return callAddToken(out, errOut, name, stateDir)

	case "list":
		if len(subArgs[1:]) > 0 {
			return ErrUnexpectedArgs
		}
		return callListTokens(out, errOut, stateDir)
	case "revoke":
		if len(subArgs[1:]) < 1 {
			return errors.New("usage: jaildeck token revoke <name>")
		}
		if len(subArgs[1:]) > 1 {
			return ErrUnexpectedArgs
		}
		name := subArgs[1]
		return callRevokeToken(out, name, stateDir)

	default:
		return fmt.Errorf("unknown command: %q", subArgs[0])
	}
}

func loadState(stateDir string) (*state.Store, state.State, error) {
	store := state.NewStore(stateDir)
	st, err := store.Load()
	// It is expected on a fresh install
	if err != nil && !errors.Is(err, state.ErrNotConfigured) {
		return nil, state.State{}, err
	}
	return store, st, nil
}

func callAddToken(out, errOut io.Writer, name, stateDir string) error {
	store, st, err := loadState(stateDir)
	if err != nil {
		return err
	}
	tokens, token, err := auth.AddToken(st.APITokens, name, time.Now())
	if err != nil {
		return err
	}
	st.APITokens = tokens
	if err := store.Save(st); err != nil {
		return withPermissionHint(err)
	}
	fmt.Fprintf(errOut, "Token generated for: %s\n", name)
	fmt.Fprintln(out, token)
	fmt.Fprintln(errOut, "Warning: This token is shown only once!")
	return nil
}

func callListTokens(out, errOut io.Writer, stateDir string) error {
	_, st, err := loadState(stateDir)
	if err != nil {
		return err
	}

	if len(st.APITokens) == 0 {
		fmt.Fprintln(errOut, "there is no token stored yet")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	defer w.Flush()

	fmt.Fprint(w, "NAME\tCREATED AT\n")

	for _, item := range st.APITokens {
		fmt.Fprintf(w, "%v\t%v\n", item.Name, item.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

func callRevokeToken(out io.Writer, name, stateDir string) error {
	store, st, err := loadState(stateDir)
	if err != nil {
		return err
	}
	tokens, err := auth.RevokeToken(st.APITokens, name)
	if err != nil {
		return err
	}
	st.APITokens = tokens
	if err := store.Save(st); err != nil {
		return withPermissionHint(err)
	}
	fmt.Fprintf(out, "token '%v' revoked successfully\n", name)

	return nil
}
