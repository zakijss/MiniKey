package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zakijss/minideck/atomicfile"
	"github.com/zakijss/minideck/ui"
)

// Settings is settings.json in the app folder. It holds only choices about
// the screens, nothing about the accounts: those stay in the vault. A
// missing field keeps the behaviour from before it existed.
type Settings struct {
	// LockMinutes is how long MiniKey waits untouched before it locks
	// itself: one of lockChoices (0 or anything else means idleLock).
	LockMinutes int `json:"lock_minutes,omitempty"`
	// HideNext hides the next code on a code's own screen (default shown).
	HideNext bool `json:"hide_next,omitempty"`
	// Sort is "name" (A to Z) or "" (the order they were imported in).
	Sort string `json:"sort,omitempty"`
}

// lockChoices are the auto-lock times Options offers, in minutes.
var lockChoices = []int{1, 3, 5, 10}

// idle is how long MiniKey waits untouched before it locks itself. A value
// that isn't one of the choices (an edited file) gives the default.
func (s Settings) idle() time.Duration {
	for _, m := range lockChoices {
		if s.LockMinutes == m {
			return time.Duration(m) * time.Minute
		}
	}
	return idleLock
}

// byName reports whether the accounts are listed A to Z.
func (s Settings) byName() bool { return s.Sort == "name" }

// loadSettings reads settings.json; a missing or damaged file gives the
// defaults.
func loadSettings(dir string) Settings {
	var s Settings
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("settings: %v", err)
		}
		return s
	}
	if err := json.Unmarshal(b, &s); err != nil {
		log.Printf("settings.json: %v", err)
		return Settings{}
	}
	return s
}

// saveSettings writes settings.json.
func saveSettings(dir string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "settings.json"), append(b, '\n'), 0644)
}

// order is the accounts' indexes in the order the list shows them.
func order(accts []Account, byName bool) []int {
	idx := make([]int, len(accts))
	for i := range idx {
		idx[i] = i
	}
	if byName {
		sort.SliceStable(idx, func(i, j int) bool {
			a, b := accts[idx[i]], accts[idx[j]]
			if ta, tb := strings.ToLower(a.Title()), strings.ToLower(b.Title()); ta != tb {
				return ta < tb
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
	}
	return idx
}

// step moves i by d through n choices, wrapping round.
func step(i, d, n int) int { return ((i+d)%n + n) % n }

// The value rows in Options, after the action rows.
const (
	rowLock = "lock"
	rowNext = "next"
	rowSort = "sort"
)

// settingRows are the rows in Options that change a setting: A and RIGHT
// step to the next value, LEFT to the one before.
func settingRows(s Settings) []ui.Item {
	lock := ui.Item{Label: "Auto-lock", Icon: "clock", Value: rowLock,
		Detail: fmt.Sprintf("%d min", int(s.idle().Minutes())), Sub: "Locks after this long untouched"}
	next := ui.Item{Label: "Next code", Icon: "eye", Value: rowNext, Detail: "Shown", Sub: "On a code's own screen"}
	if s.HideNext {
		next.Detail, next.Sub = "Hidden", "Only the current code is shown"
	}
	sorted := ui.Item{Label: "Sort accounts", Icon: "list", Value: rowSort, Detail: "Imported", Sub: "As they were imported"}
	if s.byName() {
		sorted.Detail, sorted.Sub = "Name", "A to Z, by service"
	}
	return []ui.Item{lock, next, sorted}
}

// change steps the setting on row by d (1 or -1) and returns the result.
func (s Settings) change(row any, d int) Settings {
	switch row {
	case rowLock:
		cur := 0
		for i, m := range lockChoices {
			if time.Duration(m)*time.Minute == s.idle() {
				cur = i
			}
		}
		s.LockMinutes = lockChoices[step(cur, d, len(lockChoices))]
	case rowNext:
		s.HideNext = !s.HideNext
	case rowSort:
		if s.byName() {
			s.Sort = ""
		} else {
			s.Sort = "name"
		}
	}
	return s
}

// matching keeps the positions in idx of the accounts whose service or
// account name contains q, ignoring case ("" keeps them all).
func matching(accts []Account, idx []int, q string) []int {
	if q == "" {
		return idx
	}
	q = strings.ToLower(q)
	var out []int
	for _, i := range idx {
		x := accts[i]
		if strings.Contains(strings.ToLower(x.Issuer), q) || strings.Contains(strings.ToLower(x.Name), q) {
			out = append(out, i)
		}
	}
	return out
}

// jump is the row L2 (back) or R2 (forward) moves to from row sel of a list
// whose rows are titled titles. Sorted by name, it goes to the first account
// of the next letter, or back to the first of this letter (of the one before
// when already there); in imported order, to the first or last row.
func jump(titles []string, sel int, forward, byName bool) int {
	last := len(titles) - 1
	if last < 0 {
		return 0
	}
	sel = min(max(sel, 0), last)
	if !byName {
		if forward {
			return last
		}
		return 0
	}
	letter := func(i int) string {
		for _, r := range strings.ToUpper(titles[i]) {
			return string(r)
		}
		return ""
	}
	if forward {
		for i := sel + 1; i <= last; i++ {
			if letter(i) != letter(sel) {
				return i
			}
		}
		return last
	}
	if sel > 0 && letter(sel-1) != letter(sel) {
		sel-- // already at the start of a letter: go to the one before
	}
	for sel > 0 && letter(sel-1) == letter(sel) {
		sel--
	}
	return sel
}
