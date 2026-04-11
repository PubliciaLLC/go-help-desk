package plugin

import (
	"context"
	"time"

	"github.com/open-help-desk/open-help-desk/backend/internal/domain/notification"
)

// Runtime distinguishes first-party (compiled-in) plugins from third-party
// sandboxed WASM plugins.
type Runtime string

const (
	RuntimeNative Runtime = "native"
	RuntimeWASM   Runtime = "wasm"
)

// Manifest describes a plugin's identity and capabilities.
type Manifest struct {
	ID          string   // reverse-DNS identifier, e.g. "com.example.slack-notifier"
	Name        string
	Version     string
	Description string
	Author      string
	Hooks       []notification.EventType // events this plugin subscribes to
	Runtime     Runtime
}

// Plugin is the persisted record of an installed plugin.
type Plugin struct {
	Manifest    Manifest
	Enabled     bool
	WASMPath    string    // path to .wasm file on disk; empty for native plugins
	InstalledAt time.Time
}

// Handler is the function a plugin provides to process an event.
type Handler func(ctx context.Context, event notification.Event) error

// Registry manages installed plugins and routes events to their handlers.
type Registry interface {
	// Register adds a native plugin. Called at startup for first-party plugins.
	Register(manifest Manifest, handler Handler) error

	// Dispatch sends an event to all enabled plugins subscribed to it.
	Dispatch(ctx context.Context, event notification.Event) error

	// List returns all installed plugins.
	List() []Plugin

	// Enable/Disable toggle a plugin without uninstalling it.
	Enable(id string) error
	Disable(id string) error
}

// Store persists plugin installation state.
type Store interface {
	Create(ctx context.Context, p Plugin) error
	GetByID(ctx context.Context, id string) (Plugin, error)
	Update(ctx context.Context, p Plugin) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]Plugin, error)
}
