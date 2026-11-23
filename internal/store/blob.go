package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// SaveBlob stores the exact bytes supplied and returns their SHA-256. Raw
// bytes are required for faithful replay; display surfaces call
// LoadBlobForDisplay so secrets are still masked at the API boundary.
func (s *Store) SaveBlob(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	dir := filepath.Join(s.blobDir, hash[:2])
	path := filepath.Join(dir, hash)

	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "blob-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	_, _ = s.db.Exec(`INSERT INTO blobs (hash, size, created_at) VALUES (?, ?, ?)
		ON CONFLICT(hash) DO NOTHING`, hash, len(data), now())
	return hash, nil
}

func (s *Store) LoadBlob(hash string) ([]byte, error) {
	if len(hash) != 64 {
		return nil, fmt.Errorf("invalid blob hash %q", hash)
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return nil, fmt.Errorf("invalid blob hash %q", hash)
		}
	}
	data, err := os.ReadFile(filepath.Join(s.blobDir, hash[:2], hash))
	if err != nil {
		return nil, err
	}
	// Content-addressed storage must verify the address on read: an on-disk
	// edit would otherwise be served under the original hash.
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, fmt.Errorf("blob %s failed integrity check", hash)
	}
	return data, nil
}

// LoadBlobForDisplay returns the blob with sensitive keys masked. Replay must
// use LoadBlob so the original bytes are preserved.
func (s *Store) LoadBlobForDisplay(hash string) ([]byte, error) {
	data, err := s.LoadBlob(hash)
	if err != nil {
		return nil, err
	}
	return RedactJSON(data), nil
}

// ---------- credentials ----------
