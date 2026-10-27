// Package proxyauth implements the fixed Squid Basic authentication helper.
package proxyauth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"squid-control.local/squid/internal/credentials"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

const MaxLine = 4096

// Run speaks non-concurrent Squid helper protocol. It never logs credentials and
// opens only the configured database in read-only mode, without migrations.
func Run(path string, input io.Reader, output io.Writer) error {
	return run(path, input, output, false)
}

// RunState checks the authenticated verifier generation on every request, even
// when Squid reuses Basic credentials. No password is sent to this helper.
func RunState(path string, input io.Reader, output io.Writer) error {
	return run(path, input, output, true)
}
func run(path string, input io.Reader, output io.Writer, stateOnly bool) error {
	if !filepath.IsAbs(path) {
		return errors.New("proxy authentication requires an absolute database path")
	}
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "mode=ro&_pragma=busy_timeout(1000)&_pragma=query_only(1)"
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return errors.New("proxy authentication storage unavailable")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	dummy := ""
	if !stateOnly {
		dummy, e = credentials.Hash(rand.Text())
		if e != nil {
			return errors.New("proxy authentication unavailable")
		}
	}
	reader := bufio.NewReaderSize(input, MaxLine)
	for {
		line, e := reader.ReadSlice('\n')
		if errors.Is(e, bufio.ErrBufferFull) {
			// Consume the whole oversized request, then emit exactly one denial.
			for errors.Is(e, bufio.ErrBufferFull) {
				_, e = reader.ReadSlice('\n')
			}
			if _, we := io.WriteString(output, "ERR\n"); we != nil {
				return we
			}
			if errors.Is(e, io.EOF) {
				return nil
			}
			if e != nil {
				return errors.New("proxy helper input failed")
			}
			continue
		}
		if errors.Is(e, io.EOF) {
			if len(line) > 0 {
				_, we := io.WriteString(output, "ERR\n")
				return we
			}
			return nil
		}
		if e != nil {
			return errors.New("proxy helper input failed")
		}
		lineValue := strings.TrimSuffix(string(line), "\n")
		answer := "ERR\n"
		if stateOnly {
			if checkState(db, lineValue) {
				answer = "OK\n"
			}
		} else {
			if generation := credentialGeneration(db, lineValue, dummy, credentials.Matches); generation != "" {
				answer = "OK sc_generation_=" + generation + "\n"
			}
		}
		if _, e = io.WriteString(output, answer); e != nil {
			return e
		}
	}
}
func check(db *sql.DB, line, dummy string) bool {
	return checkWithVerifier(db, line, dummy, credentials.Matches)
}
func checkWithVerifier(db *sql.DB, line, dummy string, verify func(string, string) bool) bool {
	return credentialGeneration(db, line, dummy, verify) != ""
}
func credentialGeneration(db *sql.DB, line, dummy string, verify func(string, string) bool) string {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return ""
	}
	username, e := url.PathUnescape(fields[0])
	if e != nil || !usernamePattern.MatchString(username) {
		return ""
	}
	password, e := url.PathUnescape(fields[1])
	if e != nil || len(password) < 12 || len(password) > 1024 || strings.ContainsAny(password, "\x00\r\n") {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var hash string
	var enabled bool
	e = db.QueryRowContext(ctx, "SELECT password_hash,enabled FROM proxy_accounts WHERE username=?", username).Scan(&hash, &enabled)
	missing := errors.Is(e, sql.ErrNoRows)
	if missing {
		hash = dummy
	} else if e != nil {
		return ""
	}
	matches := verify(hash, password)
	if !matches || missing || !enabled {
		return ""
	}
	// Recheck after the expensive hash to avoid accepting a password disabled or
	// reset while this request was being verified.
	var current string
	if e = db.QueryRowContext(ctx, "SELECT password_hash FROM proxy_accounts WHERE username=? AND enabled=1", username).Scan(&current); e != nil {
		return ""
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(current)) != 1 {
		return ""
	}
	return generation(hash)
}

func generation(hash string) string {
	v := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(v[:])
}
func checkState(db *sql.DB, line string) bool {
	fields := strings.Fields(line)
	if len(fields) != 3 || fields[2] != "-" {
		return false
	}
	username, e := url.PathUnescape(fields[0])
	if e != nil || !usernamePattern.MatchString(username) {
		return false
	}
	version, e := url.PathUnescape(fields[1])
	if e != nil || len(version) != 64 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var hash string
	if e = db.QueryRowContext(ctx, "SELECT password_hash FROM proxy_accounts WHERE username=? AND enabled=1", username).Scan(&hash); e != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(version), []byte(generation(hash))) == 1
}
