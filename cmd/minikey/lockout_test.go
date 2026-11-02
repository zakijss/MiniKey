package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
