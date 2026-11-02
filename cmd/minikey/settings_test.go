package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zakijss/minideck/ui"
)

// No settings file, or one from before a setting existed, keeps today's
// behaviour: three minutes, the next code shown, the imported order.
func TestSettingsDefaults(t *testing.T) {
	dir := t.TempDir()
	if s := loadSettings(dir); s != (Settings{}) || s.idle() != idleLock || s.HideNext || s.byName() {
		t.Fatalf("no file: %+v", s)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{}`), 0644)
	if s := loadSettings(dir); s.idle() != 3*time.Minute {
		t.Fatalf("empty file: %+v", s)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"lock_minutes":`), 0644)
	if s := loadSettings(dir); s != (Settings{}) {
		t.Fatalf("damaged file: %+v", s)
	}
	// an edited file can't make the wait longer than the choices allow
	for _, m := range []int{-1, 2, 60, 100000} {
		if d := (Settings{LockMinutes: m}).idle(); d != idleLock {
			t.Errorf("lock_minutes %d: %v", m, d)
		}
	}
}

func TestSettingsSaved(t *testing.T) {
	dir := t.TempDir()
	want := Settings{LockMinutes: 10, HideNext: true, Sort: "name"}
	if err := saveSettings(dir, want); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(dir); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "JBSWY3") {
		t.Fatalf("settings.json holds more than choices: %s", b)
	}
}

// LEFT and RIGHT step through the auto-lock times, wrapping round; the
// toggles flip either way.
func TestSettingsChange(t *testing.T) {
	var s Settings
	var got []time.Duration
	for range 5 {
		s = s.change(rowLock, 1)
		got = append(got, s.idle())
	}
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, time.Minute, 3 * time.Minute, 5 * time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RIGHT from 3 min: %v", got)
		}
	}
	if s = (Settings{}).change(rowLock, -1); s.idle() != time.Minute {
		t.Errorf("LEFT from 3 min: %v", s.idle())
	}
	if s = (Settings{LockMinutes: 1}).change(rowLock, -1); s.idle() != 10*time.Minute {
		t.Errorf("LEFT from 1 min: %v", s.idle())
	}
	s = Settings{}.change(rowNext, -1)
	if !s.HideNext || s.change(rowNext, 1).HideNext {
		t.Error("Next code didn't switch")
	}
	s = Settings{}.change(rowSort, 1)
	if !s.byName() || s.change(rowSort, -1).byName() {
		t.Error("Sort accounts didn't switch")
	}
	rows := settingRows(Settings{LockMinutes: 10, HideNext: true, Sort: "name"})
	if rows[0].Detail != "10 min" || rows[1].Detail != "Hidden" || rows[2].Detail != "Name" {
		t.Errorf("rows: %+v", rows)
	}
}

func TestOrder(t *testing.T) {
	accts := []Account{
		{Issuer: "GitHub", Name: "work"},
		{Name: "router admin"},
		{Issuer: "cloudflare", Name: "alice"},
		{Issuer: "GitHub", Name: "alice"},
	}
	if got := order(accts, false); !equal(got, []int{0, 1, 2, 3}) {
		t.Errorf("imported: %v", got)
	}
	// A to Z by the title, ignoring case, then by the account name
	if got := order(accts, true); !equal(got, []int{2, 3, 0, 1}) {
		t.Errorf("by name: %v", got)
	}
	if len(order(nil, true)) != 0 {
		t.Error("no accounts")
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Options › Sort accounts is saved, and the list, a code's screen and
// Remove follow the sorted rows while the vault keeps its order.
func TestSortedList(t *testing.T) {
	scr, _ := lookScreen(t)
	dir := t.TempDir()
	a := &app{scr: scr, dir: dir, accts: append([]Account(nil), lookAccts...)}
	// Y, then down to Sort accounts (the sixth row) and RIGHT
	a.input = presses(ui.Y, ui.Down, ui.Down, ui.Down, ui.Down, ui.Down, ui.Right, ui.B)
	a.run()
	if !a.cfg.byName() || !loadSettings(dir).byName() {
		t.Fatalf("sort not saved: %+v", a.cfg)
	}
	items := a.items(time.Unix(0, 0))
	if items[0].Label != "GitHub" || items[1].Label != "router admin" {
		t.Fatalf("imported order kept: %+v", items)
	}
	a.accts[0], a.accts[1] = a.accts[1], a.accts[0] // as imported: router admin first
	items = a.items(time.Unix(0, 0))
	if items[0].Label != "GitHub" {
		t.Fatalf("not sorted: %+v", items)
	}
	if a.list.Sel = 0; a.accts[a.index(0)].Issuer != "GitHub" {
		t.Fatal("row 0 isn't GitHub")
	}
	if !strings.Contains(a.options()[2].Label, "GitHub") {
		t.Fatalf("Remove names %q", a.options()[2].Label)
	}
}
