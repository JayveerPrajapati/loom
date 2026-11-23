package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// User is a named account. PasswordHash is never serialized.
type User struct {
	ID                string    `json:"id"`
	Email             string    `json:"email"`
	Name              string    `json:"name"`
	Role              string    `json:"role"`
	Disabled          bool      `json:"disabled"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	PasswordChangedAt time.Time `json:"-"`
	PasswordHash      string    `json:"-"`
}

// Session is an opaque browser session. Only the token hash is stored.
type Session struct {
	TokenHash  string    `json:"-"`
	UserID     string    `json:"userId"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"userAgent,omitempty"`
}

// APIToken is a scoped, revocable bearer credential.
type APIToken struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	Name       string     `json:"name"`
	Role       string     `json:"role"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	TokenHash  string     `json:"-"`
}

// AuthEvent is operational audit for authentication and user administration.
type AuthEvent struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Type   string    `json:"type"`
	UserID string    `json:"userId,omitempty"`
	Actor  string    `json:"actor,omitempty"`
	IP     string    `json:"ip,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

func newAuthID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf)
}

func parseAuthTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func nullTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	t := parseAuthTime(value.String)
	return &t
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers() (int, error) {
	var count int
	err := s.readRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

// CountAdmins returns the number of enabled admin accounts.
func (s *Store) CountAdmins() (int, error) {
	var count int
	err := s.readRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&count)
	return count, err
}

// CreateUser inserts an account. PasswordHash must already be derived.
func (s *Store) CreateUser(user User) (*User, error) {
	email := strings.ToLower(strings.TrimSpace(user.Email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("a valid email is required")
	}
	if user.ID == "" {
		user.ID = newAuthID("user")
	}
	if user.Role == "" {
		user.Role = "viewer"
	}
	now := now()
	_, err := s.db.Exec(`INSERT INTO users
		(id, email, name, password_hash, role, disabled, created_at, updated_at, password_changed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID, email, user.Name, user.PasswordHash, user.Role, boolInt(user.Disabled), now, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("a user with that email already exists")
		}
		return nil, err
	}
	return s.GetUser(user.ID)
}

const userColumns = `id, email, name, COALESCE(password_hash, ''), role, disabled,
	created_at, updated_at, password_changed_at`

func scanUser(row scanner) (*User, error) {
	var user User
	var disabled int
	var created, updated, changed string
	if err := row.Scan(&user.ID, &user.Email, &user.Name, &user.PasswordHash, &user.Role,
		&disabled, &created, &updated, &changed); err != nil {
		return nil, err
	}
	user.Disabled = disabled != 0
	user.CreatedAt = parseAuthTime(created)
	user.UpdatedAt = parseAuthTime(updated)
	user.PasswordChangedAt = parseAuthTime(changed)
	return &user, nil
}

// GetUser returns a user by id.
func (s *Store) GetUser(id string) (*User, error) {
	user, err := scanUser(s.readRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user %q not found", id)
	}
	return user, err
}

// GetUserByEmail returns a user by case-insensitive email.
func (s *Store) GetUserByEmail(email string) (*User, error) {
	user, err := scanUser(s.readRow(`SELECT `+userColumns+` FROM users WHERE email = ?`,
		strings.ToLower(strings.TrimSpace(email))))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user not found")
	}
	return user, err
}

// ListUsers returns all accounts.
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.read(`SELECT ` + userColumns + ` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *user)
	}
	return out, rows.Err()
}

// UpdateUserRole changes a role.
func (s *Store) UpdateUserRole(id, role string) error {
	res, err := s.db.Exec(`UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, role, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user %q not found", id)
	}
	return nil
}

// SetUserDisabled enables or disables an account.
func (s *Store) SetUserDisabled(id string, disabled bool) error {
	res, err := s.db.Exec(`UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(disabled), now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user %q not found", id)
	}
	return nil
}

// SetUserPassword updates the hash and records the change time.
func (s *Store) SetUserPassword(id, hash string) error {
	res, err := s.db.Exec(`UPDATE users SET password_hash = ?, password_changed_at = ?,
		updated_at = ? WHERE id = ?`, hash, now(), now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user %q not found", id)
	}
	return nil
}

// CreateSession stores a hashed session token.
func (s *Store) CreateSession(session Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions
		(token_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		session.TokenHash, session.UserID,
		session.CreatedAt.UTC().Format(time.RFC3339Nano),
		session.LastSeenAt.UTC().Format(time.RFC3339Nano),
		session.ExpiresAt.UTC().Format(time.RFC3339Nano),
		session.IP, session.UserAgent)
	return err
}

// GetSessionByHash returns the session and its user. Expired or orphaned
// sessions return sql.ErrNoRows.
func (s *Store) GetSessionByHash(hash string) (*Session, *User, error) {
	var session Session
	var user User
	var disabled int
	var created, seen, expires, userCreated, userUpdated, changed string
	err := s.readRow(`
		SELECT s.token_hash, s.user_id, s.created_at, s.last_seen_at, s.expires_at,
		       COALESCE(s.ip, ''), COALESCE(s.user_agent, ''),
		       u.id, u.email, u.name, COALESCE(u.password_hash, ''), u.role, u.disabled,
		       u.created_at, u.updated_at, u.password_changed_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?`, hash).Scan(
		&session.TokenHash, &session.UserID, &created, &seen, &expires,
		&session.IP, &session.UserAgent,
		&user.ID, &user.Email, &user.Name, &user.PasswordHash, &user.Role, &disabled,
		&userCreated, &userUpdated, &changed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, nil, err
	}
	session.CreatedAt = parseAuthTime(created)
	session.LastSeenAt = parseAuthTime(seen)
	session.ExpiresAt = parseAuthTime(expires)
	user.Disabled = disabled != 0
	user.CreatedAt = parseAuthTime(userCreated)
	user.UpdatedAt = parseAuthTime(userUpdated)
	user.PasswordChangedAt = parseAuthTime(changed)
	return &session, &user, nil
}

// TouchSession updates last_seen_at at most once per minute.
func (s *Store) TouchSession(hash string, at time.Time) error {
	cutoff := at.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE sessions SET last_seen_at = ?
		WHERE token_hash = ? AND last_seen_at < ?`,
		at.UTC().Format(time.RFC3339Nano), hash, cutoff)
	return err
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(hash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hash)
	return err
}

// DeleteUserSessions removes every session for a user, optionally keeping one.
func (s *Store) DeleteUserSessions(userID, keepHash string) error {
	if keepHash == "" {
		_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, userID, keepHash)
	return err
}

// DeleteExpiredSessions prunes expired rows.
func (s *Store) DeleteExpiredSessions(at time.Time) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`,
		at.UTC().Format(time.RFC3339Nano))
	return err
}

// CreateAPIToken stores a hashed bearer token.
func (s *Store) CreateAPIToken(token APIToken) (*APIToken, error) {
	if token.ID == "" {
		token.ID = newAuthID("tok")
	}
	now := now()
	_, err := s.db.Exec(`INSERT INTO api_tokens
		(id, user_id, name, token_hash, role, created_at, last_used_at, expires_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?, NULL)`,
		token.ID, token.UserID, token.Name, token.TokenHash, token.Role, now,
		nullableTime(token.ExpiresAt))
	if err != nil {
		return nil, err
	}
	token.CreatedAt = parseAuthTime(now)
	return &token, nil
}

// GetAPITokenByHash returns a token and its owning user.
func (s *Store) GetAPITokenByHash(hash string) (*APIToken, *User, error) {
	var token APIToken
	var user User
	var disabled int
	var created, lastUsed, expires, revoked, userCreated, userUpdated, changed sql.NullString
	err := s.readRow(`
		SELECT t.id, t.user_id, t.name, t.role, t.created_at, t.last_used_at, t.expires_at,
		       t.revoked_at, u.id, u.email, u.name, COALESCE(u.password_hash, ''), u.role,
		       u.disabled, u.created_at, u.updated_at, u.password_changed_at
		FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`, hash).Scan(
		&token.ID, &token.UserID, &token.Name, &token.Role, &created, &lastUsed, &expires,
		&revoked, &user.ID, &user.Email, &user.Name, &user.PasswordHash, &user.Role,
		&disabled, &userCreated, &userUpdated, &changed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, nil, err
	}
	token.CreatedAt = parseAuthTime(created.String)
	token.LastUsedAt = nullTime(lastUsed)
	token.ExpiresAt = nullTime(expires)
	token.RevokedAt = nullTime(revoked)
	user.Disabled = disabled != 0
	user.CreatedAt = parseAuthTime(userCreated.String)
	user.UpdatedAt = parseAuthTime(userUpdated.String)
	user.PasswordChangedAt = parseAuthTime(changed.String)
	return &token, &user, nil
}

// TouchAPIToken records last use.
func (s *Store) TouchAPIToken(id string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339Nano), id)
	return err
}

// ListAPITokens returns tokens for a user, or all tokens when userID is empty.
func (s *Store) ListAPITokens(userID string) ([]APIToken, error) {
	query := `SELECT id, user_id, name, role, created_at, last_used_at, expires_at, revoked_at
		FROM api_tokens`
	args := []any{}
	if userID != "" {
		query += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.read(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var token APIToken
		var created string
		var lastUsed, expires, revoked sql.NullString
		if err := rows.Scan(&token.ID, &token.UserID, &token.Name, &token.Role, &created,
			&lastUsed, &expires, &revoked); err != nil {
			return nil, err
		}
		token.CreatedAt = parseAuthTime(created)
		token.LastUsedAt = nullTime(lastUsed)
		token.ExpiresAt = nullTime(expires)
		token.RevokedAt = nullTime(revoked)
		out = append(out, token)
	}
	return out, rows.Err()
}

// GetAPIToken returns a token by id.
func (s *Store) GetAPIToken(id string) (*APIToken, error) {
	var token APIToken
	var created string
	var lastUsed, expires, revoked sql.NullString
	err := s.readRow(`SELECT id, user_id, name, role, created_at, last_used_at, expires_at,
		revoked_at FROM api_tokens WHERE id = ?`, id).Scan(
		&token.ID, &token.UserID, &token.Name, &token.Role, &created,
		&lastUsed, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("token %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	token.CreatedAt = parseAuthTime(created)
	token.LastUsedAt = nullTime(lastUsed)
	token.ExpiresAt = nullTime(expires)
	token.RevokedAt = nullTime(revoked)
	return &token, nil
}

// RevokeAPIToken marks a token unusable.
func (s *Store) RevokeAPIToken(id string) error {
	res, err := s.db.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("token %q not found or already revoked", id)
	}
	return nil
}

// RevokeAPITokensForUser revokes every active API token owned by userID.
// Password changes call this so a stolen token cannot outlive the reset.
func (s *Store) RevokeAPITokensForUser(userID string) error {
	_, err := s.db.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		now(), userID)
	return err
}

// AppendAuthEvent records an authentication or administration action.
func (s *Store) AppendAuthEvent(event AuthEvent) error {
	_, err := s.db.Exec(`INSERT INTO auth_events (at, type, user_id, actor, ip, detail)
		VALUES (?, ?, ?, ?, ?, ?)`,
		now(), event.Type, event.UserID, event.Actor, event.IP, event.Detail)
	return err
}

// ListAuthEvents returns the most recent auth events.
func (s *Store) ListAuthEvents(limit int) ([]AuthEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.read(`SELECT id, at, type, user_id, actor, ip, detail
		FROM auth_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthEvent
	for rows.Next() {
		var event AuthEvent
		var at string
		if err := rows.Scan(&event.ID, &at, &event.Type, &event.UserID, &event.Actor,
			&event.IP, &event.Detail); err != nil {
			return nil, err
		}
		event.At = parseAuthTime(at)
		out = append(out, event)
	}
	return out, rows.Err()
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
