package credentials

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/crypto/argon2"
	"io"
	"strings"
)

// Hash persists only a salted one-way Argon2id PHC verifier.
func Hash(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must be 12–1024 bytes")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return "$argon2id$v=19$m=19456,t=2,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func Matches(encoded, password string) bool {
	if len(password) > 1024 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallel uint8
	var trailing string
	if n, e := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d%s", &memory, &iterations, &parallel, &trailing); n != 3 || e != io.EOF {
		return false
	}
	if memory < 8192 || memory > 38912 || iterations < 1 || iterations > 4 || parallel < 1 || parallel > 2 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil || len(salt) != 16 {
		return false
	}
	key, e := base64.RawStdEncoding.DecodeString(parts[5])
	if e != nil || len(key) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(key, argon2.IDKey([]byte(password), salt, iterations, memory, parallel, 32)) == 1
}
