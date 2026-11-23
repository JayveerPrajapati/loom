// Package auth defines identities, roles and the password/token primitives used
// by the server. It has no dependency on the store or HTTP layers.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Role is an ordered permission level. A higher rank includes every lower one.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleEditor   Role = "editor"
	RoleAdmin    Role = "admin"
)

// ParseRole validates a role name.
func ParseRole(value string) (Role, bool) {
	switch role := Role(strings.ToLower(strings.TrimSpace(value))); role {
	case RoleViewer, RoleOperator, RoleEditor, RoleAdmin:
		return role, true
	default:
		return "", false
	}
}

// Rank returns the ordering position; unknown roles rank below viewer.
func (r Role) Rank() int {
	switch r {
	case RoleAdmin:
		return 4
	case RoleEditor:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

// Allows reports whether r satisfies the minimum role.
func (r Role) Allows(min Role) bool { return r.Rank() > 0 && r.Rank() >= min.Rank() }

// Principal is an authenticated caller.
type Principal struct {
	Type    string // user, token, service, anonymous
	ID      string // user id, token id, or "loom-token"
	UserID  string // owning user for token principals
	Email   string
	Role    Role
	Session string // session token hash, for logout
}

// Actor is the stable attribution string stored on runs and events. A human
// email is preferred so decision history reads "Rejected by ada@example.com"
// instead of an opaque id; principals without one fall back to their type.
func (p Principal) Actor() string {
	if email := strings.TrimSpace(p.Email); email != "" {
		return email
	}
	switch p.Type {
	case "user":
		return "user:" + p.ID
	case "token":
		return "token:" + p.ID
	case "service":
		return "service:loom-token"
	case "anonymous":
		return "anonymous"
	default:
		return "unknown"
	}
}

const (
	// SessionCookie holds the opaque session value.
	SessionCookie = "loom_session"
	// CSRFCookie is readable by the UI (double-submit); the header must match.
	CSRFCookie = "loom_csrf"
	// CSRFHeader is required on cookie-authenticated mutations.
	CSRFHeader = "X-CSRF-Token"
)

// NewToken returns a 256-bit URL-safe random value.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns the hex SHA-256 used as the at-rest lookup key.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Equal compares two strings in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword derives an argon2id hash with a random salt.
func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", fmt.Errorf("password must be at least 8 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against an encoded argon2id hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}
	var memory uint32
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// DummyHash is a valid hash used to equalise timing when the email is unknown.
func DummyHash() string {
	dummyOnce.Do(func() {
		dummyHash, _ = HashPassword("loom-timing-dummy-password")
	})
	return dummyHash
}
