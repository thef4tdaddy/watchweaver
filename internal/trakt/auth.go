package trakt

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Status string

const (
	StatusNotConfigured Status = "not_configured"
	StatusNotAuthorized Status = "not_authorized"
	StatusPending       Status = "authorization_pending"
	StatusConnected     Status = "connected"
	StatusReauth        Status = "reauth_required"
)

type Config struct {
	ClientID, ClientSecret, BaseURL string
	HTTPClient                      *http.Client
}

type PublicStatus struct {
	Status          Status `json:"status"`
	UserCode        string `json:"user_code,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
}

type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	CreatedAt    int64  `json:"created_at"`
}

type Service struct {
	db      *sql.DB
	cfg     Config
	mu      sync.Mutex
	pending *DeviceCode
}

func NewService(db *sql.DB, cfg Config) *Service {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.trakt.tv"
	}
	return &Service{db: db, cfg: cfg}
}

func (s *Service) Status(ctx context.Context) PublicStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status(ctx)
}

func (s *Service) status(ctx context.Context) PublicStatus {
	if s.cfg.ClientID == "" || s.cfg.ClientSecret == "" {
		return PublicStatus{Status: StatusNotConfigured}
	}
	if s.pending != nil {
		return PublicStatus{Status: StatusPending, UserCode: s.pending.UserCode, VerificationURL: s.pending.VerificationURL}
	}
	var access string
	err := s.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='access_token'`).Scan(&access)
	if err == nil && access != "" {
		return PublicStatus{Status: StatusConnected}
	}
	var reauth string
	if err := s.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='reauth_required'`).Scan(&reauth); err == nil && reauth == "1" {
		return PublicStatus{Status: StatusReauth}
	}
	return PublicStatus{Status: StatusNotAuthorized}
}

func (s *Service) Start(ctx context.Context) (PublicStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.ClientID == "" || s.cfg.ClientSecret == "" {
		return PublicStatus{Status: StatusNotConfigured}, errors.New("trakt is not configured")
	}
	var out DeviceCode
	if err := s.post(ctx, "/oauth/device/code", map[string]string{"client_id": s.cfg.ClientID}, &out); err != nil {
		return PublicStatus{}, err
	}
	if out.DeviceCode == "" || out.UserCode == "" || out.VerificationURL == "" || out.ExpiresIn <= 0 {
		return PublicStatus{}, errors.New("invalid device authorization response")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	s.pending = &out
	return s.status(ctx), nil
}

func (s *Service) Poll(ctx context.Context) (PublicStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return s.status(ctx), errors.New("no authorization pending")
	}
	var tok token
	err := s.post(ctx, "/oauth/device/token", map[string]string{"code": s.pending.DeviceCode, "client_id": s.cfg.ClientID, "client_secret": s.cfg.ClientSecret}, &tok)
	if err != nil {
		var remote *remoteError
		if errors.As(err, &remote) {
			switch remote.Code {
			case http.StatusBadRequest:
				return s.status(ctx), nil
			case http.StatusConflict:
				s.pending.Interval += 5
				return s.status(ctx), nil
			case http.StatusGone, http.StatusTeapot:
				s.pending = nil
				return s.status(ctx), fmt.Errorf("authorization ended: %w", err)
			}
		}
		return s.status(ctx), err
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		return s.status(ctx), errors.New("invalid token response")
	}
	if err := s.persistToken(ctx, tok); err != nil {
		return s.status(ctx), err
	}
	s.pending = nil
	return s.status(ctx), nil
}

func (s *Service) Refresh(ctx context.Context) error {
	var refresh string
	if err := s.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='refresh_token'`).Scan(&refresh); err != nil {
		return fmt.Errorf("load refresh token: %w", err)
	}
	var tok token
	if err := s.post(ctx, "/oauth/token", map[string]string{"refresh_token": refresh, "client_id": s.cfg.ClientID, "client_secret": s.cfg.ClientSecret, "grant_type": "refresh_token", "redirect_uri": "urn:ietf:wg:oauth:2.0:oob"}, &tok); err != nil {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','reauth_required','1') ON CONFLICT(integration,state_key) DO UPDATE SET state_value='1',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`)
		return err
	}
	return s.persistToken(ctx, tok)
}

func (s *Service) persistToken(ctx context.Context, tok token) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := map[string]string{"access_token": tok.AccessToken, "refresh_token": tok.RefreshToken, "reauth_required": "0"}
	for k, v := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt',?,?) ON CONFLICT(integration,state_key) DO UPDATE SET state_value=excluded.state_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type remoteError struct{ Code int }

func (e *remoteError) Error() string { return fmt.Sprintf("trakt returned HTTP %d", e.Code) }

func (s *Service) post(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("trakt request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return &remoteError{Code: resp.StatusCode}
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode trakt response: %w", err)
	}
	return nil
}

func (s *Service) PollInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return 0
	}
	return time.Duration(s.pending.Interval) * time.Second
}
