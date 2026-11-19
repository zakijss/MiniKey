package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zakijss/minideck/ui"
)

func TestFailWait(t *testing.T) {
	for fails, want := range map[int]time.Duration{0: 0, 2: 0, 3: 5 * time.Second, 4: 10 * time.Second, 9: maxWait, 50: maxWait} {
		if got := failWait(fails); got != want {
			t.Errorf("failWait(%d) = %v, want %v", fails, got, want)
		}
	}
	if maxWait != 320*time.Second {
		t.Errorf("maxWait = %v", maxWait)
	}
}

// The count and the wait outlast closing and reopening MiniKey, and the
// right code clears them.
func TestLockoutSurvivesRestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lockout.json")
	c := clock{wall: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), boot: "b1", up: time.Hour}
	if l := loadLockout(p, c); l.Fails != 0 || l.remaining(c) != 0 {
		t.Fatalf("no file: %+v", l)
	}
	l := loadLockout(p, c)
	for range 4 {
		l.fail(c)
		if err := l.save(p); err != nil {
			t.Fatal(err)
		}
	}
	// reopened 3 s later
	later := clock{wall: c.wall.Add(3 * time.Second), boot: "b1", up: c.up + 3*time.Second}
	l = loadLockout(p, later)
	if l.Fails != 4 || l.remaining(later) != 7*time.Second {
		t.Fatalf("after restart: fails %d, %v left", l.Fails, l.remaining(later))
	}
	l.fail(later) // the next wrong code waits longer, not 5 s again
	if got := l.remaining(later); got != 20*time.Second {
		t.Errorf("fifth wrong code: %v", got)
	}
	if err := resetLockout(p); err != nil {
		t.Fatal(err)
	}
	if l := loadLockout(p, later); l.Fails != 0 {
		t.Errorf("not reset: %+v", l)
	}
	if err := resetLockout(p); err != nil {
		t.Errorf("reset twice: %v", err)
	}
}

// A damaged record counts as the most wrong codes, not none.
func TestLockoutDamaged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lockout.json")
	c := clock{wall: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), boot: "b1", up: time.Hour}
	for _, junk := range []string{"", "{", "null garbage", `{"fails":-3}`} {
		os.WriteFile(p, []byte(junk), 0600)
		l := loadLockout(p, c)
		if l.Fails != maxFails || l.remaining(c) != maxWait {
			t.Errorf("%q: fails %d, %v left", junk, l.Fails, l.remaining(c))
		}
	}
}

// Setting the clock doesn't end the wait within the same boot, and a clock
// that was far ahead when the record was written can't stretch it.
func TestLockoutClockJumps(t *testing.T) {
	c := clock{wall: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), boot: "b1", up: time.Hour}
	var l lockout
	for range 9 {
		l.fail(c)
	}
	forward := clock{wall: c.wall.Add(24 * time.Hour), boot: "b1", up: c.up + 10*time.Second}
	if got := l.remaining(forward); got != maxWait-10*time.Second {
		t.Errorf("clock set a day forward: %v left", got)
	}
	// after a restart only the wall clock is known
	back := clock{wall: c.wall.Add(-48 * time.Hour), boot: "b2", up: 30 * time.Second}
	if got := l.remaining(back); got != maxWait {
		t.Errorf("clock set back: %v left, want at most %v", got, maxWait)
	}
	if got := l.remaining(clock{wall: c.wall.Add(maxWait), boot: "b2"}); got != 0 {
		t.Errorf("wait over: %v left", got)
	}
	l.Until = c.wall.Add(1000 * time.Hour) // written with the clock far ahead
	if got := l.remaining(clock{wall: c.wall, boot: "b2"}); got != maxWait {
		t.Errorf("record from the future: %v left", got)
	}
}

// A wrong code is on the card before the next try, and the right one
// clears it.
func TestUnlockCounts(t *testing.T) {
	scr, _ := lookScreen(t)
	dir := t.TempDir()
	dev := []byte("device key")
	if err := saveVault(filepath.Join(dir, "vault.enc"), dev, "UDLRAB", lookAccts); err != nil {
		t.Fatal(err)
	}
	a := &app{scr: scr, dir: dir, devkey: dev}
	lockPath := filepath.Join(dir, "lockout.json")
	// the count on the card while the vault is checked: the try is counted
	// before, so switching off during the slow check doesn't escape it
	var during []int
	var fail error
	t.Cleanup(func() { openVault = loadVault })
	openVault = func(path string, devkey []byte, code string) ([]Account, error) {
		during = append(during, loadLockout(lockPath, readClock()).Fails)
		if fail != nil {
			return nil, fail
		}
		return loadVault(path, devkey, code)
	}
	a.input = presses(ui.Up, ui.Down, ui.Left, ui.Right, ui.A, ui.A, ui.Start)
	if a.unlock(filepath.Join(dir, "vault.enc")) {
		t.Fatal("opened with the wrong code")
	}
	if l := loadLockout(lockPath, readClock()); l.Fails != 1 || len(during) != 1 || during[0] != 1 {
		t.Fatalf("after a wrong code: %+v, counted during the check: %v", l, during)
	}
	// a vault that can't be read isn't a wrong code
	fail = errors.New("input/output error")
	a.input = presses(ui.Up, ui.Down, ui.Left, ui.Right, ui.A, ui.B, ui.Start, ui.B)
	if a.unlock(filepath.Join(dir, "vault.enc")) {
		t.Fatal("opened an unreadable vault")
	}
	if l := loadLockout(lockPath, readClock()); l.Fails != 1 || during[1] != 2 {
		t.Fatalf("after a vault error: %+v, counted during the check: %v", l, during)
	}
	fail = nil
	a.input = presses(ui.Up, ui.Down, ui.Left, ui.Right, ui.A, ui.B, ui.Start)
	if !a.unlock(filepath.Join(dir, "vault.enc")) || len(a.accts) != len(lookAccts) {
		t.Fatal("didn't open with the right code")
	}
	if during[2] != 2 {
		t.Errorf("right code: counted during the check: %v", during)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("count kept after the right code: %v", err)
	}
}

// Presses made during the wait after wrong codes are dropped, not typed
// into the next code: the relay holds them one at a time.
func TestDrainRelay(t *testing.T) {
	in := make(chan ui.Event, 8)
	for range 5 {
		in <- ui.Event{Button: ui.A, Pressed: true}
	}
	a := &app{}
	relay := a.watch(in)
	for len(in) == 5 { // the relay holds the first
		time.Sleep(time.Millisecond)
	}
	drain(relay)
	select {
	case ev := <-relay:
		t.Fatalf("left after draining: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
	close(in)
	drain(relay) // a closed input doesn't hang it
}

// After a dialog waited longer than the auto-lock time, MiniKey locks
// rather than showing the codes to whoever pressed a button.
func TestStaleLocks(t *testing.T) {
	scr, _ := lookScreen(t)
	a := &app{scr: scr, accts: lookAccts, input: presses(ui.A)}
	a.mustLock.Store(true)
	a.run()
	if len(a.input) != 1 {
		t.Error("went on after a long wait")
	}
	a.mustLock.Store(false)
	a.run()
	if len(a.input) != 0 {
		t.Error("locked after a short wait")
	}
}

// Someone who finds Options open after the auto-lock time can't raise
// Auto-lock to get past it: the first press after the wait locks, before
// the setting changes.
func TestRaisingAutoLockStillLocks(t *testing.T) {
	scr, _ := lookScreen(t)
	in := make(chan ui.Event, 8) // a send never waits on a menu that has closed
	a := &app{scr: scr, dir: t.TempDir(), accts: lookAccts}
	a.setCfg(Settings{})
	a.idle.Store(int64(time.Second)) // auto-lock after 1 s, for the test
	a.input = a.watch(in)
	done := make(chan bool)
	go func() { done <- a.settings() }()
	for range 4 { // down to Auto-lock, at once
		in <- ui.Event{Button: ui.Down, Pressed: true}
	}
	time.Sleep(2 * time.Second) // left alone (the menu's own timer counts minutes)
	in <- ui.Event{Button: ui.Right, Pressed: true}
	in <- ui.Event{Button: ui.B, Pressed: true} // taken by the relay either way
	if <-done {
		t.Fatal("Options stayed open")
	}
	if a.cfg != (Settings{}) || !a.stale() {
		t.Fatalf("setting changed to %+v, stale %v", a.cfg, a.stale())
	}
	if _, err := os.Stat(filepath.Join(a.dir, "settings.json")); err == nil {
		t.Fatal("settings saved")
	}
}
