package store

import (
	"crypto/rand"
	"fmt"
	"time"
)

type CredentialInfo struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	CreatedAt  time.Time `json:"createdAt"`
	UsageCount int       `json:"usageCount"`
}

func (s *Store) SaveCredential(id, typ string, plaintext []byte) error {
	s.keyMu.RLock()
	current := s.currentKey
	s.keyMu.RUnlock()
	nonce := make([]byte, current.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := current.aead.Seal(nonce, nonce, plaintext, nil)
	_, err := s.db.Exec(`
		INSERT INTO credentials (id, type, data, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET type = excluded.type, data = excluded.data`,
		id, typ, sealed, now())
	return err
}

func (s *Store) ListCredentials() ([]CredentialInfo, error) {
	rows, err := s.read(`SELECT id, type, created_at FROM credentials ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialInfo
	for rows.Next() {
		var item CredentialInfo
		var created string
		if err := rows.Scan(&item.ID, &item.Type, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCredential(id string) error {
	res, err := s.db.Exec(`DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("credential %q not found", id)
	}
	return nil
}

func (s *Store) GetCredential(id string) (string, []byte, error) {
	var typ string
	var sealed []byte
	err := s.readRow(`SELECT type, data FROM credentials WHERE id = ?`, id).Scan(&typ, &sealed)
	if err != nil {
		return "", nil, err
	}
	// openSealed tries every key in the keyring: credentials saved before a
	// rotation (or mid-rotation) stay readable.
	plaintext, err := s.openSealed(sealed)
	if err != nil {
		return "", nil, err
	}
	return typ, plaintext, nil
}

// ---------- helpers ----------
