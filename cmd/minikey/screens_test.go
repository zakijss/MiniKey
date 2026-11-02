package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zakijss/minideck/ui"
)

// TestScreens draws the app's screens to PNG files for review. It runs
// only with MINIDECK_GALLERY=DIR set.
func TestScreens(t *testing.T) {
	dir := os.Getenv("MINIDECK_GALLERY")
	if dir == "" {
		t.Skip("set MINIDECK_GALLERY=DIR to draw the screens")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	accts := []Account{
		{Issuer: "GitHub", Name: "alice", Secret: "JBSWY3DPEHPK3PXP", Algo: "SHA1", Digits: 6, Period: 30},
		{Issuer: "Cloudflare", Name: "alice@example.com", Secret: "JBSWY3DPEHPK3PXQ", Algo: "SHA256", Digits: 8, Period: 60},
		{Name: "router admin", Secret: "JBSWY3DPEHPK3PXR", Algo: "SHA1", Digits: 6, Period: 30},
	}
	shot := func(name string, f func(*app)) {
		t.Setenv("MINIDECK_UI_PNG", filepath.Join(dir, "minikey-"+name+".png"))
		scr, err := ui.Open()
		if err != nil {
			t.Fatal(err)
		}
		in := make(chan ui.Event)
		close(in) // screens that wait for input draw once and return
		f(&app{scr: scr, input: in, accts: accts})
	}
	shot("1-code", func(a *app) { a.drawCode("Enter your unlock code", "That code didn't open the vault.", 4) })
	shot("2-list", func(a *app) { a.drawList() })
	shot("3-list-60s", func(a *app) { a.list.Sel = 1; a.drawList() }) // countdown follows the 60 s account
	shot("4-detail", func(a *app) { a.list.Sel = 1; a.detail() })
	shot("5-options", func(a *app) { a.settings() })
	shot("6-options-set", func(a *app) { a.cfg = Settings{LockMinutes: 10, HideNext: true, Sort: "name"}; a.settings() })
	shot("6-options-end", func(a *app) {
		a.input = presses(ui.Up, ui.Up) // to Sort accounts
		a.settings()
	})
	shot("7-detail-no-next", func(a *app) { a.cfg.HideNext = true; a.detail() })
	shot("8-list-by-name", func(a *app) { a.cfg.Sort = "name"; a.drawList() })
}
