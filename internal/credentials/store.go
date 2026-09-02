package credentials

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const keySize = 32

type Overrides struct {
	TraktClientID     string
	TraktClientSecret string
	DiscordWebhookURL string
}

type Store struct {
	db        *sql.DB
	aead      cipher.AEAD
	overrides Overrides
}

func Open(db *sql.DB, keyPath string, overrides Overrides) (*Store, error) {
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize credential encryption: %w", err)
	}
	store := &Store{db: db, aead: aead, overrides: Overrides{
		TraktClientID: strings.TrimSpace(overrides.TraktClientID), TraktClientSecret: strings.TrimSpace(overrides.TraktClientSecret), DiscordWebhookURL: strings.TrimSpace(overrides.DiscordWebhookURL),
	}}
	if err := store.migrateLegacy(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) migrateLegacy(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT state_key,state_value FROM integration_state WHERE integration='trakt' AND state_key IN ('access_token','refresh_token')`)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for key, value := range values {
		if value != "" {
			if err := s.Set(ctx, "trakt", key, value); err != nil {
				return err
			}
		}
	}
	if len(values) > 0 {
		_, err = s.db.ExecContext(ctx, `DELETE FROM integration_state WHERE integration='trakt' AND state_key IN ('access_token','refresh_token')`)
	}
	return err
}

func DefaultKeyPath(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), ".watchweaver.key")
}

func BackupKey(source, destination string) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read credential key for backup: %w", err)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create credential key backup: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("write credential key backup: %w", err)
	}
	return file.Close()
}

func loadOrCreateKey(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		key, decodeErr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if decodeErr != nil || len(key) != keySize {
			return nil, fmt.Errorf("credential key is invalid")
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read credential key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create credential key directory: %w", err)
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate credential key: %w", err)
	}
	encoded := []byte(base64.RawStdEncoding.EncodeToString(key) + "\n")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return loadOrCreateKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create credential key: %w", err)
	}
	if _, err = file.Write(encoded); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write credential key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close credential key: %w", err)
	}
	return key, nil
}

func (s *Store) Set(ctx context.Context, integration, key, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("credential value is required")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate credential nonce: %w", err)
	}
	aad := []byte(integration + ":" + key)
	ciphertext := s.aead.Seal(nonce, nonce, []byte(value), aad)
	_, err := s.db.ExecContext(ctx, `INSERT INTO encrypted_credentials(integration,credential_key,ciphertext) VALUES(?,?,?) ON CONFLICT(integration,credential_key) DO UPDATE SET ciphertext=excluded.ciphertext,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, integration, key, ciphertext)
	return err
}

func (s *Store) Get(ctx context.Context, integration, key string) (string, error) {
	if value, overridden := s.override(integration, key); overridden {
		return value, nil
	}
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `SELECT ciphertext FROM encrypted_credentials WHERE integration=? AND credential_key=?`, integration, key).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(ciphertext) < s.aead.NonceSize() {
		return "", fmt.Errorf("stored credential is invalid")
	}
	nonce, sealed := ciphertext[:s.aead.NonceSize()], ciphertext[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, sealed, []byte(integration+":"+key))
	if err != nil {
		return "", fmt.Errorf("decrypt stored credential: %w", err)
	}
	return string(plain), nil
}

func (s *Store) DeleteIntegration(ctx context.Context, integration string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM encrypted_credentials WHERE integration=?`, integration)
	return err
}

func (s *Store) IsOverridden(integration, key string) bool {
	_, ok := s.override(integration, key)
	return ok
}

func (s *Store) Configured(ctx context.Context, integration string, keys ...string) (bool, error) {
	for _, key := range keys {
		value, err := s.Get(ctx, integration, key)
		if err != nil || value == "" {
			return false, err
		}
	}
	return true, nil
}

func (s *Store) override(integration, key string) (string, bool) {
	var value string
	switch integration + ":" + key {
	case "trakt:client_id":
		value = s.overrides.TraktClientID
	case "trakt:client_secret":
		value = s.overrides.TraktClientSecret
	case "discord:webhook_url":
		value = s.overrides.DiscordWebhookURL
	}
	return value, value != ""
}
