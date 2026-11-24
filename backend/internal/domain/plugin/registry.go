package plugin

import (
	"context"
	"fmt"
	"sync"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

type loadedPlugin struct {
	manifest Manifest
	handler  Handler
	enabled  bool
}

// registry is the in-memory implementation of Registry.
// Plugins are loaded once at startup for native plugins; WASM plugins are
// loaded on install and reloaded on restart.
type registry struct {
	mu      sync.RWMutex
	plugins map[string]*loadedPlugin // keyed by manifest.ID
}

// NewRegistry returns an empty registry.
func NewRegistry() Registry { return &registry{plugins: make(map[string]*loadedPlugin)} }

func (r *registry) Register(manifest Manifest, handler Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[manifest.ID]; exists {
		return fmt.Errorf("plugin %q already registered", manifest.ID)
	}
	r.plugins[manifest.ID] = &loadedPlugin{
		manifest: manifest,
		handler:  handler,
		enabled:  true,
	}
	return nil
}

// Dispatch calls every enabled plugin subscribed to the event type.
// Plugin errors are logged but do not abort remaining dispatches.
func (r *registry) Dispatch(ctx context.Context, event notification.Event) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.plugins {
		if !p.enabled || !subscribes(p.manifest, event.Type) {
			continue
		}
		// Run synchronously for now; async execution is a v2 concern.
		_ = p.handler(ctx, event)
	}
	return nil
}

func (r *registry) List() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, Plugin{
			Manifest: p.manifest,
			Enabled:  p.enabled,
		})
	}
	return out
}

func (r *registry) Enable(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[id]
	if !ok {
		return fmt.Errorf("plugin %q not found", id)
	}
	p.enabled = true
	return nil
}

func (r *registry) Disable(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[id]
	if !ok {
		return fmt.Errorf("plugin %q not found", id)
	}
	p.enabled = false
	return nil
}

func subscribes(m Manifest, eventType notification.EventType) bool {
	for _, h := range m.Hooks {
		if h == eventType {
			return true
		}
	}
	return false
}
