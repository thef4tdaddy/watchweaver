package jellyfinremote

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

// Pool runs independent outbound event streams for any number of Jellyfin servers.
type Pool struct {
	client   *http.Client
	accept   Accepter
	mu       sync.RWMutex
	managers map[string]*Manager
	running  map[string]bool
	ctx      context.Context
}

func NewPool(client *http.Client, accept Accepter) *Pool {
	return &Pool{client: client, accept: accept, managers: make(map[string]*Manager), running: make(map[string]bool)}
}

func (p *Pool) Run(ctx context.Context) error {
	p.mu.Lock()
	p.ctx = ctx
	managers := make([]*Manager, 0, len(p.managers))
	for id, manager := range p.managers {
		managers = append(managers, manager)
		p.running[id] = true
	}
	p.mu.Unlock()
	for _, manager := range managers {
		go runManager(ctx, manager)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (p *Pool) Configure(id string, cfg Config) {
	p.mu.Lock()
	manager := p.managers[id]
	start := false
	if manager == nil {
		manager = New(p.client, p.accept)
		p.managers[id] = manager
		start = p.ctx != nil
		p.running[id] = start
	}
	ctx := p.ctx
	p.mu.Unlock()
	manager.Configure(cfg)
	if ctx != nil && start {
		go runManager(ctx, manager)
	}
}

func (p *Pool) Remove(id string) {
	p.mu.Lock()
	manager := p.managers[id]
	delete(p.managers, id)
	delete(p.running, id)
	p.mu.Unlock()
	if manager != nil {
		manager.Configure(Config{})
	}
}

func (p *Pool) Status(id string) (Status, bool) {
	p.mu.RLock()
	manager := p.managers[id]
	p.mu.RUnlock()
	if manager == nil {
		return Status{}, false
	}
	return manager.Status(), true
}

func (p *Pool) Test(ctx context.Context, cfg Config) (string, error) {
	return New(p.client, p.accept).Test(ctx, cfg)
}

func runManager(ctx context.Context, manager *Manager) {
	if err := manager.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return
	}
}
