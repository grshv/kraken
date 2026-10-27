package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Read from a TTY without echo, or explicitly from a bounded stdin pipe.
// No password flags, environment variables, temporary credential files or logs.
func readPassword(stdin bool) (string, error) {
	if stdin {
		raw, e := io.ReadAll(io.LimitReader(os.Stdin, 1027))
		if e != nil {
			return "", e
		}
		defer clear(raw)
		value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		if len(value) > 1024 {
			return "", errors.New("password too long")
		}
		return value, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("interactive terminal required; for a pipe use --password-stdin")
	}
	fmt.Fprint(os.Stderr, "Новый пароль: ")
	first, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if e != nil {
		return "", e
	}
	defer clear(first)
	fmt.Fprint(os.Stderr, "Повторите пароль: ")
	second, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if e != nil {
		return "", e
	}
	defer clear(second)
	if !bytes.Equal(first, second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}
