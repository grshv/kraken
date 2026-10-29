// Package proxyauth implements the fixed Squid Basic authentication helper.
//
// The helper reads a JSON export of enabled proxy accounts rather than the
// panel SQLite store: Squid runs as a separate UID that must not open the
// panel database. The Go process atomically rewrites the export after every
// proxy-account mutation and at serve start.
package proxyauth

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"squid-control.local/squid/internal/credentials"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

const MaxLine = 4096

// Account is one enabled proxy credential in the export read by Squid.
type Account struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type exportFile struct {
	Version  int       `json:"version"`
	Accounts []Account `json:"accounts"`
}

// WriteExport atomically replaces path with the given enabled accounts. The
// file is group-readable (0640) so the proxy user sharing the runtime
// directory group can read it, while its parent is private to the panel user.
func WriteExport(path string, accounts []Account) error {
	b, e := json.Marshal(exportFile{Version: 1, Accounts: accounts})
	if e != nil {
		return e
	}
	return atomicWrite(path, b, 0640)
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".stage-")
	if e != nil {
		return e
	}
	name := f.Name()
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(name)
		}
	}()
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	renamed = true
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

// Run speaks non-concurrent Squid helper protocol. It never logs credentials
// and reads the export on every request so a reset/disable takes effect on the
// next line without restarting the helper.
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
		return errors.New("proxy authentication requires an absolute export path")
	}
	dummy := ""
	if !stateOnly {
		h, e := credentials.Hash(rand.Text())
		if e != nil {
			return errors.New("proxy authentication unavailable")
		}
		dummy = h
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
			if checkState(path, lineValue) {
				answer = "OK\n"
			}
		} else {
			if generation := credentialGeneration(path, lineValue, dummy, credentials.Matches); generation != "" {
				answer = "OK sc_generation_=" + generation + "\n"
			}
		}
		if _, e = io.WriteString(output, answer); e != nil {
			return e
		}
	}
}

// fileCache memoizes the parsed export per file identity. WriteExport renames
// a new file into place, so a reset is seen even when size and mtime match.
var (
	fileCacheMu sync.Mutex
	fileCache   = map[string]fileCacheEntry{}
)

type fileCacheEntry struct {
	info     os.FileInfo
	accounts []Account
}

func loadAccounts(path string) ([]Account, error) {
	st, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	fileCacheMu.Lock()
	if entry, ok := fileCache[path]; ok && os.SameFile(entry.info, st) && entry.info.ModTime().Equal(st.ModTime()) && entry.info.Size() == st.Size() {
		fileCacheMu.Unlock()
		return entry.accounts, nil
	}
	fileCacheMu.Unlock()
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	if st, e = file.Stat(); e != nil {
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(file, 16<<20))
	if e != nil {
		return nil, e
	}
	var f exportFile
	if e := json.Unmarshal(b, &f); e != nil {
		return nil, e
	}
	fileCacheMu.Lock()
	fileCache[path] = fileCacheEntry{info: st, accounts: f.Accounts}
	fileCacheMu.Unlock()
	return f.Accounts, nil
}

func findAccount(accounts []Account, username string) (Account, bool) {
	for _, a := range accounts {
		if a.Username == username {
			return a, true
		}
	}
	return Account{}, false
}

func credentialGeneration(path, line, dummy string, verify func(string, string) bool) string {
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
	accounts, e := loadAccounts(path)
	if e != nil {
		// Absent or corrupt export: still run the dummy verify so an attacker
		// cannot learn whether a username exists via timing. The fresh read
		// below fails and the request is denied.
		accounts = nil
	}
	acct, ok := findAccount(accounts, username)
	hash := dummy
	if ok {
		hash = acct.PasswordHash
	}
	if !verify(hash, password) {
		return ""
	}
	if !ok {
		return ""
	}
	// Recheck after the expensive hash to avoid accepting a password disabled or
	// reset while this request was being verified.
	fresh, e := loadAccounts(path)
	if e != nil {
		return ""
	}
	cur, ok := findAccount(fresh, username)
	if !ok || subtle.ConstantTimeCompare([]byte(hash), []byte(cur.PasswordHash)) != 1 {
		return ""
	}
	return generation(hash)
}

func generation(hash string) string {
	v := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(v[:])
}

func checkState(path, line string) bool {
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
	accounts, e := loadAccounts(path)
	if e != nil {
		return false
	}
	acct, ok := findAccount(accounts, username)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(version), []byte(generation(acct.PasswordHash))) == 1
}
