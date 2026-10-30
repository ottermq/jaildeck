package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
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
		name, scopes, err := addArgs(subArgs[1:])
		if err != nil {
			return err
		}
		return callAddToken(out, errOut, name, scopes, stateDir)

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

const addUsage = "usage: jaildeck token add <name> [--scope <scope>]..."

// addArgs reads a name and any number of --scope flags, in either order:
// `token add otter --scope console` and `token add --scope console otter`.
func addArgs(args []string) (string, []string, error) {
	var name string
	names := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--scope":
			if i+1 >= len(args) {
				return "", nil, errors.New(addUsage)
			}
			i++
			names = append(names, args[i])
		case strings.HasPrefix(arg, "--scope="):
			names = append(names, strings.TrimPrefix(arg, "--scope="))
		case strings.HasPrefix(arg, "-"):
			return "", nil, fmt.Errorf("unknown flag %q; %s", arg, addUsage)
		case name != "":
			return "", nil, ErrUnexpectedArgs
		default:
			name = arg
		}
	}
	if name == "" {
		return "", nil, errors.New(addUsage)
	}
	scopes, err := auth.ParseScopes(names)
	if err != nil {
		return "", nil, err
	}
	return name, scopes, nil
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

func callAddToken(out, errOut io.Writer, name string, scopes []string, stateDir string) error {
	store, st, err := loadState(stateDir)
	if err != nil {
		return err
	}
	tokens, token, err := auth.AddToken(st.APITokens, name, scopes, time.Now())
	if err != nil {
		return err
	}
	st.APITokens = tokens
	if err := store.Save(st); err != nil {
		return withPermissionHint(err)
	}
	fmt.Fprintf(errOut, "Token generated for: %s, with %s\n", name, scopesText(scopes))
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

	fmt.Fprint(w, "NAME\tCREATED AT\tSCOPES\n")

	for _, item := range st.APITokens {
		scopes := strings.Join(item.Scopes, ",")
		switch {
		case item.Scopes == nil:
			scopes = "all (made before scopes)"
		case len(item.Scopes) == 0:
			scopes = "-"
		}
		fmt.Fprintf(w, "%v\t%v\t%v\n", item.Name, item.CreatedAt.Format(time.RFC3339), scopes)
	}
	return nil
}

func scopesText(scopes []string) string {
	if len(scopes) == 0 {
		return "no scopes: the plain API only"
	}
	return "the scopes " + strings.Join(scopes, ", ")
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
