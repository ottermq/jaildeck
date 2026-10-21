package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ottermq/jaildeck/internal/auth"
	"golang.org/x/term"
)

const minPasswordLen = 6

var (
	ErrEmptyPassword  = errors.New("password cannot be empty")
	ErrShortPassword  = fmt.Errorf("the password must have at least %d characters", minPasswordLen)
	ErrUnexpectedArgs = errors.New("unexpected arguments")
)

func runPasswd(args []string, stdin io.Reader, out io.Writer, stateDir string) error {
	if len(args) < 2 {
		return errors.New("expected passwd command")
	}
	fs := flag.NewFlagSet("passwd", flag.ContinueOnError)
	fs.SetOutput(out)
	fromStdin := fs.Bool("stdin", false, "Read password from standard input")

	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 0 {
		return fmt.Errorf("%w: %v", ErrUnexpectedArgs, fs.Args())
	}

	var password string

	if *fromStdin {
		reader := bufio.NewReader(stdin)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read password: %w", err)
		}
		password = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")

	} else {
		terminal, ok := stdin.(*os.File)
		if !ok || !term.IsTerminal(int(terminal.Fd())) {
			return errors.New("interactive password entry requires a terminal; use --stdin for piped input")
		}

		read := func(prompt string) ([]byte, error) {
			if _, err := fmt.Fprint(out, prompt); err != nil {
				return nil, err
			}
			value, err := term.ReadPassword(int(terminal.Fd()))
			fmt.Fprintln(out)
			return value, err
		}

		first, err := read("New password: ")
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		second, err := read("Repeat password: ")
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		if !bytes.Equal(first, second) {
			return errors.New("passwords do not match")
		}
		password = string(first)
	}

	if err := passwordValidation(password); err != nil {
		return err
	}

	hash, err := auth.Hash(password)
	if err != nil {
		return err
	}

	store, st, err := loadState(stateDir)
	if err != nil {
		return err
	}
	st.PasswordHash = hash
	if err := store.Save(st); err != nil {
		return withPermissionHint(err)
	}
	fmt.Fprint(out, "password successfully stored\n")
	return nil
}

func withPermissionHint(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w\n(run as root, or set JAILDECK_STATE_DIR to a writable path)", err)
	}
	return err
}

func passwordValidation(password string) error {
	if password == "" {
		return ErrEmptyPassword
	}

	if len(password) < minPasswordLen {
		return ErrShortPassword
	}
	return nil
}
