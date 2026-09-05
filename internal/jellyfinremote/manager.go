package jellyfinremote

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/jellyfin"
)

type Config struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	UserID  string `json:"user_id"`
	APIKey  string `json:"-"`
}

type Status struct {
	Configured      bool       `json:"configured"`
	Enabled         bool       `json:"enabled"`
	Connected       bool       `json:"connected"`
	URL             string     `json:"url,omitempty"`
	UserID          string     `json:"user_id,omitempty"`
	LastConnectedAt *time.Time `json:"last_connected_at,omitempty"`
	LastEventAt     *time.Time `json:"last_event_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	ReconnectCount  int64      `json:"reconnect_count"`
	EventsReceived  int64      `json:"events_received"`
	ProtocolVersion int        `json:"protocol_version"`
}

type Accepter interface {
	Accept(context.Context, jellyfin.Event) (jellyfin.Result, error)
}

type Manager struct {
	client *http.Client
	accept Accepter
	mu     sync.RWMutex
	config Config
	status Status
	wake   chan struct{}
}

func New(client *http.Client, accept Accepter) *Manager {
	if client == nil {
		client = &http.Client{}
	}
	return &Manager{client: client, accept: accept, wake: make(chan struct{}, 1), status: Status{ProtocolVersion: 1}}
}

func (m *Manager) Configure(cfg Config) {
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.UserID = strings.TrimSpace(cfg.UserID)
	m.mu.Lock()
	m.config = cfg
	m.status.Configured = cfg.URL != "" && cfg.APIKey != ""
	m.status.Enabled = cfg.Enabled
	m.status.URL = cfg.URL
	m.status.UserID = cfg.UserID
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Status() Status { m.mu.RLock(); defer m.mu.RUnlock(); return m.status }

func (m *Manager) Test(ctx context.Context, cfg Config) (string, error) {
	base, err := validateURL(cfg.URL)
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/System/Info", nil)
	req.Header.Set("X-Emby-Token", cfg.APIKey)
	res, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to Jellyfin: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Jellyfin returned HTTP %d", res.StatusCode)
	}
	var info struct {
		Version string `json:"Version"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&info); err != nil {
		return "", fmt.Errorf("read Jellyfin response: %w", err)
	}
	return info.Version, nil
}

func (m *Manager) Run(ctx context.Context) error {
	backoff := time.Second
	for ctx.Err() == nil {
		m.mu.RLock()
		cfg := m.config
		m.mu.RUnlock()
		if !cfg.Enabled || cfg.URL == "" || cfg.APIKey == "" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-m.wake:
				continue
			}
		}
		err := m.consume(ctx, cfg)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.mu.Lock()
		m.status.Connected = false
		if err != nil {
			m.status.LastError = safeError(err)
			m.status.ReconnectCount++
		}
		m.mu.Unlock()
		log.Printf("Jellyfin remote stream disconnected; retry_in=%s reason=%s", backoff, safeError(err))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.wake:
			backoff = time.Second
		case <-time.After(backoff):
			if backoff < time.Minute {
				backoff *= 2
			}
		}
	}
	return ctx.Err()
}

func (m *Manager) consume(ctx context.Context, cfg Config) error {
	base, err := validateURL(cfg.URL)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/watchweaver/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Emby-Token", cfg.APIKey)
	res, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Jellyfin event stream returned HTTP %d", res.StatusCode)
	}
	now := time.Now().UTC()
	m.mu.Lock()
	m.status.Connected = true
	m.status.LastConnectedAt = &now
	m.status.LastError = ""
	m.mu.Unlock()
	log.Printf("Jellyfin remote stream connected")
	err = parseSSE(res.Body, func(name string, data []byte) error {
		if name != "watchweaver.event" {
			return nil
		}
		var event jellyfin.Event
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("decode Jellyfin stream event: %w", err)
		}
		result, err := m.accept.Accept(ctx, event)
		if err != nil {
			return fmt.Errorf("accept Jellyfin stream event: %w", err)
		}
		now := time.Now().UTC()
		m.mu.Lock()
		m.status.LastEventAt = &now
		m.status.EventsReceived++
		m.mu.Unlock()
		log.Printf("Jellyfin remote event accepted: event_id=%q duplicate=%t", event.EventID, result.Duplicate)
		return nil
	})
	if err == nil {
		return io.EOF
	}
	return err
}

func parseSSE(r io.Reader, receive func(string, []byte) error) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 1<<20)
	name := "message"
	var data []string
	flush := func() error {
		if len(data) == 0 {
			name = "message"
			return nil
		}
		err := receive(name, []byte(strings.Join(data, "\n")))
		name = "message"
		data = nil
		return err
	}
	for s.Scan() {
		line := s.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	return nil
}

func validateURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("Jellyfin URL must be an absolute HTTP or HTTPS URL")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func safeError(err error) string {
	if err == nil {
		return "connection closed"
	}
	value := err.Error()
	if len(value) > 240 {
		return value[:240]
	}
	return value
}
