package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// keyEntry is one signing/encryption key in the keyring. macKey signs the
// event chain and chain tags; aead seals credentials. Both derive from the
// same raw secret so the keyring is one unit of rotation.
type keyEntry struct {
	id     string
	macKey []byte
	aead   cipher.AEAD
}

// deriveKeys turns a raw secret into the MAC and AEAD key material. The
// derivations match the pre-keyring single-key format byte for byte, so
// importing an existing secret.key keeps old chains and credentials valid.
func deriveKeys(secret []byte) (macKey []byte, aead cipher.AEAD, err error) {
	enc := sha256.Sum256(secret)
	macSeed := append(append([]byte(nil), secret...), []byte("loom-event-chain-v1")...)
	macSum := sha256.Sum256(macSeed)
	block, err := aes.NewCipher(enc[:])
	if err != nil {
		return nil, nil, err
	}
	aead, err = cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	return macSum[:], aead, nil
}

func newKeyEntry(id string, secret []byte) (keyEntry, error) {
	macKey, aead, err := deriveKeys(secret)
	if err != nil {
		return keyEntry{}, err
	}
	return keyEntry{id: id, macKey: macKey, aead: aead}, nil
}

// keyIDFor derives a stable id from the secret itself, so importing the same
// secret twice can never mint two keyring entries.
func keyIDFor(secret []byte) string {
	sum := sha256.Sum256(secret)
	return "k" + hex.EncodeToString(sum[:4])
}

// initPinnedKey runs the keyring with a single secret supplied by the
// environment (LOOM_SECRET or LOOM_SECRET_KEY_FILE). A pinned secret cannot
// be rotated in place; rotation requires the on-disk keyring.
func (s *Store) initPinnedKey(secret []byte, id string) error {
	entry, err := newKeyEntry(id, secret)
	if err != nil {
		return err
	}
	s.keys = map[string]keyEntry{id: entry}
	s.currentKey = entry
	s.legacyKey = entry
	s.pinned = true
	return nil
}

// initKeyring loads or creates the on-disk keyring under <dir>/keys/. A
// pre-keyring deployment is bootstrapped by importing dir/secret.key as the
// first entry; the legacy id is recorded so runs with a NULL key_id (rows
// written before the keyring existed) keep verifying against the key that
// actually signed them. secret.key itself is left in place so a downgrade
// can still read it.
func (s *Store) initKeyring(dir string) error {
	keyDir := filepath.Join(dir, "keys")
	legacyPath := filepath.Join(dir, "secret.key")
	if _, err := os.Stat(keyDir); errors.Is(err, os.ErrNotExist) {
		var secret []byte
		if data, rerr := os.ReadFile(legacyPath); rerr == nil && len(data) > 0 {
			secret = data
		} else {
			secret = make([]byte, 32)
			if _, gerr := rand.Read(secret); gerr != nil {
				return gerr
			}
		}
		id := keyIDFor(secret)
		if merr := os.MkdirAll(keyDir, 0o700); merr != nil {
			return merr
		}
		if werr := os.WriteFile(filepath.Join(keyDir, id+".key"), secret, 0o600); werr != nil {
			return werr
		}
		if werr := os.WriteFile(filepath.Join(keyDir, "current"), []byte(id), 0o600); werr != nil {
			return werr
		}
		if werr := os.WriteFile(filepath.Join(keyDir, "legacy"), []byte(id), 0o600); werr != nil {
			return werr
		}
	}
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		return err
	}
	keys := map[string]keyEntry{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".key") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(keyDir, name))
		if rerr != nil {
			return rerr
		}
		id := strings.TrimSuffix(name, ".key")
		entry, cerr := newKeyEntry(id, data)
		if cerr != nil {
			return cerr
		}
		keys[id] = entry
	}
	if len(keys) == 0 {
		return fmt.Errorf("keyring %s contains no keys", keyDir)
	}
	currentID := strings.TrimSpace(string(mustRead(filepath.Join(keyDir, "current"))))
	current, ok := keys[currentID]
	if !ok {
		return fmt.Errorf("keyring: current key %q not found in %s", currentID, keyDir)
	}
	legacyKey := current
	if data, rerr := os.ReadFile(filepath.Join(keyDir, "legacy")); rerr == nil {
		id := strings.TrimSpace(string(data))
		if entry, ok := keys[id]; ok {
			legacyKey = entry
		}
	}
	s.keys = keys
	s.currentKey = current
	s.legacyKey = legacyKey
	return nil
}

func mustRead(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// keyFor resolves the key a run was signed with. An empty id means the row
// predates the keyring and was signed by the legacy (bootstrap) key. An id
// that is not in the keyring is a hard error: the chain must fail closed
// rather than silently re-bless events under a different key.
func (s *Store) keyFor(id string) (keyEntry, error) {
	if id == "" {
		s.keyMu.RLock()
		entry := s.legacyKey
		s.keyMu.RUnlock()
		return entry, nil
	}
	s.keyMu.RLock()
	entry, ok := s.keys[id]
	s.keyMu.RUnlock()
	if !ok {
		return keyEntry{}, fmt.Errorf("signing key %q not found in keyring", id)
	}
	return entry, nil
}

// openSealed decrypts a credential sealed under any key in the keyring, so
// mixed rows during a rotation are still readable.
func (s *Store) openSealed(sealed []byte) ([]byte, error) {
	try := func(entry keyEntry) ([]byte, error) {
		nonceSize := entry.aead.NonceSize()
		if len(sealed) < nonceSize {
			return nil, errors.New("credential is corrupt")
		}
		return entry.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	}
	s.keyMu.RLock()
	current := s.currentKey
	entries := make([]keyEntry, 0, len(s.keys))
	for _, entry := range s.keys {
		entries = append(entries, entry)
	}
	s.keyMu.RUnlock()
	if plaintext, err := try(current); err == nil {
		return plaintext, nil
	}
	for _, entry := range entries {
		if plaintext, err := try(entry); err == nil {
			return plaintext, nil
		}
	}
	return nil, errors.New("credential is corrupt")
}

// RotateKey generates a fresh key, re-seals every credential under it, and
// makes it the signing key for new runs. Old keys stay in the keyring so the
// chains of runs recorded under them keep verifying; runs already executing
// finish signing with the key recorded on their row. The write order is
// crash-safe: the new key file lands first (an unused key file is harmless),
// credentials are re-sealed in a transaction, and the current pointer flips
// last.
func (s *Store) RotateKey() (string, error) {
	if s.pinned {
		return "", errors.New("secret is pinned by LOOM_SECRET/LOOM_SECRET_KEY_FILE; rotation requires the on-disk keyring")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	id := keyIDFor(secret)
	entry, err := newKeyEntry(id, secret)
	if err != nil {
		return "", err
	}
	keyDir := filepath.Join(s.dir, "keys")
	if err := os.WriteFile(filepath.Join(keyDir, id+".key"), secret, 0o600); err != nil {
		return "", err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id, data FROM credentials`)
	if err != nil {
		return "", err
	}
	type pending struct {
		id     string
		sealed []byte
	}
	var resealed []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.sealed); err != nil {
			rows.Close()
			return "", err
		}
		resealed = append(resealed, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	for _, p := range resealed {
		plaintext, err := s.openSealed(p.sealed)
		if err != nil {
			return "", fmt.Errorf("credential %q: %w", p.id, err)
		}
		nonce := make([]byte, entry.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return "", err
		}
		sealed := entry.aead.Seal(nonce, nonce, plaintext, nil)
		if _, err := tx.Exec(`UPDATE credentials SET data = ? WHERE id = ?`, sealed, p.id); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}

	if err := os.WriteFile(filepath.Join(keyDir, "current"), []byte(id), 0o600); err != nil {
		return "", err
	}
	s.keyMu.Lock()
	s.keys[id] = entry
	s.currentKey = entry
	s.keyMu.Unlock()
	return id, nil
}

// KeyInfo describes the keyring for `loom key list`.
type KeyInfo struct {
	ID      string
	Current bool
	Legacy  bool
}

// ListKeys returns every keyring entry, the current signing key first.
func (s *Store) ListKeys() []KeyInfo {
	s.keyMu.RLock()
	ids := make([]string, 0, len(s.keys))
	current, legacy := s.currentKey.id, s.legacyKey.id
	for id := range s.keys {
		ids = append(ids, id)
	}
	s.keyMu.RUnlock()
	sort.Strings(ids)
	out := make([]KeyInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, KeyInfo{
			ID:      id,
			Current: id == current,
			Legacy:  id == legacy,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// stampLegacyKeyIDs backfills the key id onto rows written before the
// keyring existed. Runs created after the keyring always carry their signing
// key id; this heals NULL rows (including a NULL that a database-only
// attacker re-introduces, which would otherwise resolve to the legacy key
// anyway).
func (s *Store) stampLegacyKeyIDs() error {
	_, err := s.db.Exec(`UPDATE runs SET key_id = ? WHERE key_id IS NULL`, s.legacyKey.id)
	return err
}
