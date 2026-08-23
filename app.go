package main

import (
	"context"
)

// App holds application lifecycle state shared across services.
type App struct {
	ctx context.Context
}

// NewApp creates the App.
func NewApp() *App {
	return &App{}
}

// startup saves the runtime context so services can emit Wails events.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}
