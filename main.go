package main

import (
	"embed"
	"fmt"
	"log"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"masque/internal/character"
	"masque/internal/chat"
	"masque/internal/datadir"
	"masque/internal/devlog"
	"masque/internal/ollamamgr"
	"masque/internal/settings"
	"masque/internal/store"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dataDir, err := datadir.Resolve()
	if err != nil {
		return fmt.Errorf("resolving data dir: %w", err)
	}
	st, err := store.Open(filepath.Join(dataDir, "masque.db"))
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Printf("closing store: %v", cerr)
		}
	}()

	app := NewApp()
	requestLog := devlog.New()
	settingsSvc := settings.NewService(st)
	chatSvc := chat.NewService(st, app.emit, requestLog)
	characterSvc := character.NewService(st)
	ollamaMgrSvc := ollamamgr.NewService(st, app.emit)
	devlogSvc := devlog.NewService(requestLog)

	err = wails.Run(&options.App{
		Title:  "Masque",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 10, G: 10, B: 11, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
			settingsSvc,
			chatSvc,
			characterSvc,
			ollamaMgrSvc,
			devlogSvc,
		},
	})
	if err != nil {
		return fmt.Errorf("running app: %w", err)
	}
	return nil
}
