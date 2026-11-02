package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zakijss/minideck/ui"
)

// presses is input that presses each button in turn, then ends.
func presses(bs ...ui.Button) <-chan ui.Event {
	ch := make(chan ui.Event, len(bs))
	for _, b := range bs {
		ch <- ui.Event{Button: b, Pressed: true}
	}
	close(ch)
	return ch
}

// lookScreen opens a screen that draws nowhere, with the shared look saved
// to a temporary file. The look in use is put back after the test.
func lookScreen(t *testing.T) (*ui.Screen, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "appearance.json")
	t.Setenv("MINIDECK_UI_NULL", "1")
	t.Setenv("MINIDECK_APPEARANCE", path)
	scr, err := ui.Open()
	if err != nil {
		t.Fatal(err)
	}
	start := ui.CurrentAppearance()
	t.Cleanup(func() { scr.SetAppearance(start) })
	return scr, path
}

// wantLookChanged checks that Appearance was opened: RIGHT on its first
// row moved to the next theme, and B saved it.
func wantLookChanged(t *testing.T, start ui.Appearance, path string) {
	t.Helper()
	if ui.CurrentAppearance().Theme == start.Theme {
		t.Errorf("theme still %q: Appearance didn't open", start.Theme)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("look not saved: %v", err)
	}
}

var lookAccts = []Account{
	{Issuer: "GitHub", Name: "alice", Secret: "JBSWY3DPEHPK3PXP", Algo: "SHA1", Digits: 6, Period: 30},
	{Name: "router admin", Secret: "JBSWY3DPEHPK3PXR", Algo: "SHA1", Digits: 6, Period: 30},
}

// SELECT on the list of codes opens Appearance.
func TestSelectOpensAppearance(t *testing.T) {
	scr, path := lookScreen(t)
	start := ui.CurrentAppearance()
	a := &app{scr: scr, input: presses(ui.Select, ui.Right, ui.B, ui.B), accts: lookAccts}
	a.run()
	wantLookChanged(t, start, path)
}

// Options ends with Appearance, and choosing it opens the screen.
func TestOptionsOpenAppearance(t *testing.T) {
	scr, path := lookScreen(t)
	start := ui.CurrentAppearance()
	a := &app{scr: scr, accts: lookAccts}
	if items := a.options(); items[len(items)-1].Label != "Appearance" {
		t.Fatalf("Options end with %+v", items[len(items)-1])
	}
	// UP from the first row wraps to the last
	a.input = presses(ui.Y, ui.Up, ui.A, ui.Right, ui.B, ui.B)
	a.run()
	wantLookChanged(t, start, path)
}
