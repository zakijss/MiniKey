package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zakijss/minideck/atomicfile"
)

// After three wrong codes each try earns a wait, doubling from 5 s up to
// maxWait. The count and the end of the wait are kept on the card, so
// closing and reopening MiniKey doesn't start them again.

const (
	freeTries = 3
	maxFails  = freeTries + 6
	maxWait   = 5 * time.Second << (maxFails - freeTries)
)

// failWait is the wait earned by the fails-th wrong code in a row.
func failWait(fails int) time.Duration {
	if fails < freeTries {
		return 0
	}
	return 5 * time.Second << (min(fails, maxFails) - freeTries)
}

// clock is the time read two ways: the wall clock, which anyone holding
// the device can set, and the time since boot, which can't be set, with
// the ID of that boot.
type clock struct {
	wall time.Time
	boot string
	up   time.Duration
}

// readClock reads the clock. Without /proc (on a computer) only the wall
// clock is filled in.
func readClock() clock {
	c := clock{wall: time.Now()}
	id, err1 := os.ReadFile("/proc/sys/kernel/random/boot_id")
	up, err2 := os.ReadFile("/proc/uptime")
	var sec float64
	if err1 == nil && err2 == nil {
		if _, err := fmt.Sscan(string(up), &sec); err == nil {
			c.boot = strings.TrimSpace(string(id))
			c.up = time.Duration(sec * float64(time.Second))
		}
	}
	return c
}

// lockout is the record of wrong codes since the last right one.
type lockout struct {
	Fails int       `json:"fails"`
	Until time.Time `json:"until"` // when the wait ends, by the wall clock
	// Boot and UpUntil say when the wait ends by the time since boot, which
	// setting the clock forward doesn't change.
	Boot    string        `json:"boot,omitempty"`
	UpUntil time.Duration `json:"up_until,omitempty"`
}

// loadLockout reads the record. A missing file means no wrong codes. A
// damaged one can't be told from one tampered with, so it counts as the
// most wrong codes, with the longest wait still to go.
func loadLockout(path string, now clock) lockout {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return lockout{}
	}
	var l lockout
	if err != nil || json.Unmarshal(b, &l) != nil || l.Fails < 0 {
		return lockout{Fails: maxFails, Until: now.wall.Add(maxWait), Boot: now.boot, UpUntil: now.up + maxWait}
	}
	return l
}

// fail counts a wrong code and starts the wait it earned.
func (l *lockout) fail(now clock) {
	l.Fails = min(l.Fails+1, maxFails)
	w := failWait(l.Fails)
	l.Until, l.Boot, l.UpUntil = now.wall.Add(w), now.boot, now.up+w
}

// remaining is how much of the wait is left. Within the boot the record was
// written in, the time since boot decides, so changing the clock in
// Onion's settings doesn't shorten it; after a restart only the wall clock
// is left to go by. Either way it is never longer than the wait earned, so
// a clock that was ahead when the record was written can't lock the vault
// for longer than that.
func (l lockout) remaining(now clock) time.Duration {
	var left time.Duration
	if l.Boot != "" && l.Boot == now.boot {
		left = l.UpUntil - now.up
	} else {
		left = l.Until.Sub(now.wall)
	}
	return min(max(left, 0), failWait(l.Fails))
}

// save writes the record.
func (l lockout) save(path string) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, b, 0600)
}

// resetLockout removes the record, after the right code.
func resetLockout(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
