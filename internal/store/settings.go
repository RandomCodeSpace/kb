package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// AISettings is the client-visible view of a user's AI configuration; the
// API key itself is never exposed, only whether one is stored.
type AISettings struct {
	BaseURL string
	Model   string
	HasKey  bool
}

// AISettings returns the user's AI settings; a user with no stored row gets
// the zero value.
func (s *Store) AISettings(user string) (AISettings, error) {
	var st AISettings
	var enc []byte
	switch err := s.db.QueryRow(`SELECT ai_base_url, ai_model, ai_key_enc FROM settings WHERE user = ?`, user).Scan(&st.BaseURL, &st.Model, &enc); {
	case errors.Is(err, sql.ErrNoRows):
		return AISettings{}, nil
	case err != nil:
		return AISettings{}, fmt.Errorf("store: ai settings: %w", err)
	}
	st.HasKey = len(enc) > 0
	return st, nil
}

// SetAISettings patches the user's AI settings; nil fields are left
// unchanged. A non-nil empty apiKey clears the stored key; a non-empty one
// is AES-GCM encrypted with the store secret before it is written.
//
// Changing the base URL to a different scheme or host without supplying a
// key in the same call also clears the stored key (reported via
// keyCleared): a stored credential must never follow a re-pointed endpoint,
// or whoever can write settings could route the decrypted key to a host
// they control.
func (s *Store) SetAISettings(user string, baseURL, model *string, apiKey *string) (keyCleared bool, err error) {
	err = s.withTx(func(tx *sql.Tx) error {
		var base, mdl string
		var enc []byte
		switch err := tx.QueryRow(`SELECT ai_base_url, ai_model, ai_key_enc FROM settings WHERE user = ?`, user).Scan(&base, &mdl, &enc); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("store: read ai settings: %w", err)
		}
		if baseURL != nil {
			if apiKey == nil && len(enc) > 0 && !sameAIOrigin(base, *baseURL) {
				enc = nil
				keyCleared = true
			}
			base = *baseURL
		}
		if model != nil {
			mdl = *model
		}
		if apiKey != nil {
			if *apiKey == "" {
				enc = nil
			} else {
				sealed, err := s.seal([]byte(*apiKey))
				if err != nil {
					return err
				}
				enc = sealed
			}
		}
		if _, err := tx.Exec(`INSERT INTO settings (user, ai_base_url, ai_model, ai_key_enc) VALUES (?, ?, ?, ?)
			ON CONFLICT(user) DO UPDATE SET ai_base_url = excluded.ai_base_url, ai_model = excluded.ai_model, ai_key_enc = excluded.ai_key_enc`,
			user, base, mdl, enc); err != nil {
			return fmt.Errorf("store: write ai settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return keyCleared, nil
}

// sameAIOrigin reports whether two base URLs share scheme and host (incl.
// port) — the condition under which a stored API key may be kept across a
// base-URL change. Unparsable URLs count as a different origin.
func sameAIOrigin(a, b string) bool {
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host)
}

// AIKey returns the user's decrypted API key, or "" when none is stored.
func (s *Store) AIKey(user string) (string, error) {
	var enc []byte
	switch err := s.db.QueryRow(`SELECT ai_key_enc FROM settings WHERE user = ?`, user).Scan(&enc); {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("store: ai key: %w", err)
	}
	if len(enc) == 0 {
		return "", nil
	}
	plain, err := s.openSealed(enc)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// newAEAD builds the AES-256-GCM cipher for a secret of any length by
// deriving the key as SHA-256(secret).
func newAEAD(secret []byte) (cipher.AEAD, error) {
	key := sha256.Sum256(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("store: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: gcm: %w", err)
	}
	return aead, nil
}

// seal encrypts plain, prepending the random nonce to the ciphertext.
func (s *Store) seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("store: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

// openSealed reverses seal.
func (s *Store) openSealed(enc []byte) ([]byte, error) {
	ns := s.aead.NonceSize()
	if len(enc) < ns {
		return nil, errors.New("store: ciphertext too short")
	}
	plain, err := s.aead.Open(nil, enc[:ns], enc[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("store: decrypt key: %w", err)
	}
	return plain, nil
}

// LoadOrCreateSecret returns the AES secret: the raw bytes of the KB_SECRET
// environment variable when set and non-empty, otherwise the contents of
// <dataDir>/secret, which is created with 32 random bytes and mode 0600
// (dataDir included, mode 0700) when absent.
func LoadOrCreateSecret(dataDir string) ([]byte, error) {
	if v, ok := os.LookupEnv("KB_SECRET"); ok && v != "" {
		return []byte(v), nil
	}
	path := filepath.Join(dataDir, "secret")
	b, err := os.ReadFile(path)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("store: read secret: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("store: generate secret: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, fmt.Errorf("store: write secret: %w", err)
	}
	return b, nil
}
