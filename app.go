package main

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App holds application lifecycle state shared across services.
type App struct {
	mu  sync.Mutex
	ctx context.Context
}

// NewApp creates the App.
func NewApp() *App {
	return &App{}
}

// startup saves the runtime context so services can emit Wails events.
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
}

// emit forwards an event to the frontend, dropping it if the runtime
// isn't up yet. Safe to call from any goroutine.
func (a *App) emit(event string, args ...any) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		runtime.EventsEmit(ctx, event, args...)
	}
}
