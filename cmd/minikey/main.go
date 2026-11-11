// MiniKey shows 2FA codes (TOTP). Accounts are imported once from a plain
// otpauth:// export (Ente Auth and others), sealed with the device key plus
// an unlock code entered on the buttons, and the plain-text export is
// deleted.
package main

import (
	"fmt"
	"image"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zakijss/minideck/appdir"
	"github.com/zakijss/minideck/secrets"
	"github.com/zakijss/minideck/tnet"
	"github.com/zakijss/minideck/ui"
	"github.com/zakijss/minideck/version"
)

const (
	minCode  = 6
	maxCode  = 16
	idleLock = 3 * time.Minute // unless Options › Auto-lock says otherwise
)

// The eight buttons an unlock code is made of, and how each is shown.
var codeKeys = map[ui.Button]byte{
	ui.Up: 'U', ui.Down: 'D', ui.Left: 'L', ui.Right: 'R',
	ui.A: 'A', ui.B: 'B', ui.X: 'X', ui.Y: 'Y',
}

// importDirs is where MiniKey looks for an export: its own folder and the
// root of the SD card.
func importDirs(dir string) []string {
	if v := os.Getenv("KEYFOB_IMPORT_DIRS"); v != "" {
		return strings.Split(v, ":")
	}
	return []string{dir, "/mnt/SDCARD"}
}

type app struct {
	scr    *ui.Screen
	input  <-chan ui.Event
	dir    string
	devkey []byte
	code   string
	accts  []Account
	list   ui.List
	cfg    Settings
	// filter is the X search: only the accounts whose names contain it
	// are listed ("" lists them all).
	filter string
	// skew is how far the device clock is behind internet time, once
	// checked (codes are only as right as the clock).
	skew    atomic.Int64
	checked atomic.Bool
}

// maxSkew is how far off the clock may be before MiniKey corrects for it.
const maxSkew = 15 * time.Second

// now is the time codes are made for: the device clock, corrected when it
// is known to be off.
func (a *app) now() time.Time {
	d := time.Duration(a.skew.Load())
	if d > maxSkew || d < -maxSkew {
		return time.Now().Add(d)
	}
	return time.Now()
}

// checkClock compares the device clock with the Date header of an HTTPS
// server (certificates checked), without changing the system clock.
func (a *app) checkClock() {
	c := tnet.Client(4 * time.Second)
	for _, u := range []string{"https://www.google.com/generate_204", "https://cloudflare.com/cdn-cgi/trace"} {
		t0 := time.Now()
		resp, err := c.Get(u)
		if err != nil {
			continue
		}
		resp.Body.Close()
		d, err := http.ParseTime(resp.Header.Get("Date"))
		if err != nil {
			continue
		}
		// Date has whole seconds: take the middle of that second, and of
		// the round trip.
		local := t0.Add(time.Since(t0) / 2)
		a.skew.Store(int64(d.Add(500 * time.Millisecond).Sub(local)))
		a.checked.Store(true)
		return
	}
}

// clockNote is shown under the title when the clock was corrected.
func (a *app) clockNote() string {
	d := time.Duration(a.skew.Load())
	if d <= maxSkew && d >= -maxSkew {
		return ""
	}
	dir := "slow"
	if d < 0 {
		d, dir = -d, "fast"
	}
	return fmt.Sprintf(" · clock %s %s, corrected", span(d), dir)
}

// span writes a duration the way people say it: 40 s, 12 min, 8 h, 8 h 5 min.
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute).Minutes()))
	}
	m := int(d.Round(time.Minute).Minutes())
	if m%60 == 0 {
		return fmt.Sprintf("%d h", m/60)
	}
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}

func main() {
	version.Flag("minikey")
	version.Note("minikey")
	scr, err := ui.Open()
	if err != nil {
		log.Fatal(err)
	}
	defer scr.Close()
	a := &app{scr: scr, input: ui.Input(), dir: appdir.Dir()}
	a.cfg = loadSettings(a.dir)
	exit := ui.Hint{Button: "B", Label: "Exit"}

	if time.Now().Year() < 2025 {
		scr.Message("MiniKey", "The clock isn't set, so codes would be wrong. Turn on Tweaks › System › Date and time › Set automatically via the internet, connect to Wi-Fi, then open MiniKey again.", exit)
		waitExit(a.input)
		return
	}
	if err := secrets.EnsureDeviceKey(); err != nil {
		log.Printf("device key: %v", err)
	} else if a.devkey, err = secrets.DeviceKey(); err != nil {
		log.Printf("device key: %v", err)
	}
	if a.devkey == nil {
		scr.Message("MiniKey", "Can't read the device key in internal storage, so the vault can't be opened.", exit)
		waitExit(a.input)
		return
	}

	go a.checkClock() // while the code is typed
	vault := filepath.Join(a.dir, "vault.enc")
	if _, err := os.Stat(vault); os.IsNotExist(err) {
		if !scr.Confirm(a.input, "MiniKey", "Choose an unlock code: 6 to 16 presses of the d-pad and A, B, X, Y. Longer is safer; 10 or more is best.", "Set code") {
			return
		}
		code, ok := a.newCode("Choose an unlock code")
		if !ok {
			return
		}
		a.code = code
		if !a.save() {
			return
		}
	} else if !a.unlock(vault) {
		return
	}
	a.importNew(false)
	a.run()
}

func waitExit(input <-chan ui.Event) {
	for ev := range input {
		if ev.Pressed && (ev.Button == ui.B || ev.Button == ui.Menu) {
			return
		}
	}
}

// ---- unlock code entry ----

// readCode asks for a code. START finishes, SELECT deletes the last press,
// MENU cancels.
func (a *app) readCode(title, msg string) (string, bool) {
	var code []byte
	for {
		a.drawCode(title, msg, len(code))
		ev, ok, frame := a.scr.Next(a.input)
		if !ok {
			return "", false
		}
		if frame || !ev.Pressed || ev.Repeat {
			continue
		}
		if k, isKey := codeKeys[ev.Button]; isKey {
			if len(code) < maxCode {
				code = append(code, k)
			}
			msg = ""
			continue
		}
		switch ev.Button {
		case ui.Select:
			if len(code) > 0 {
				code = code[:len(code)-1]
			}
		case ui.Start:
			if len(code) > 0 {
				return string(code), true
			}
		case ui.Menu:
			return "", false
		}
	}
}

func (a *app) drawCode(title, msg string, n int) {
	s := a.scr
	s.Background()
	top := s.Header("MiniKey", title)
	// one dot per press, empty rings for the minimum still to go
	slots := max(n, minCode)
	gap := 34
	x0 := ui.W/2 - (slots-1)*gap/2
	cy := top + 70
	for i := 0; i < slots; i++ {
		x := float64(x0 + i*gap)
		if i < n {
			s.Circle(x, float64(cy), 10, ui.Text)
		} else {
			s.Ring(x, float64(cy), 10, 2, 1, ui.Line)
		}
	}
	s.TextCenter(ui.W/2, cy+28, fmt.Sprintf("%d presses", n), ui.Small, ui.Muted)
	if msg != "" {
		for i, l := range ui.Wrap(ui.Body, msg, 520) {
			s.TextCenter(ui.W/2, cy+64+i*ui.LineHeight(ui.Body), l, ui.Body, ui.Warn)
		}
	}
	s.TextCenter(ui.W/2, ui.H-110, "Use the d-pad and A, B, X, Y", ui.Small, ui.Muted)
	s.Footer(ui.Hint{Button: "START", Label: "Done"}, ui.Hint{Button: "SELECT", Label: "Delete"}, ui.Hint{Button: "MENU", Label: "Cancel"})
	s.Present()
}

// newCode asks for a new code twice.
func (a *app) newCode(title string) (string, bool) {
	msg := ""
	for {
		c1, ok := a.readCode(title, msg)
		if !ok {
			return "", false
		}
		if len(c1) < minCode {
			msg = fmt.Sprintf("Use at least %d presses.", minCode)
			continue
		}
		c2, ok := a.readCode("Enter the same code again", "")
		if !ok {
			return "", false
		}
		if c1 != c2 {
			msg = "The two codes didn't match. Try again."
			continue
		}
		return c1, true
	}
}

// unlock asks for the code until the vault opens. Each wrong try makes the
// next wait longer, and the count outlasts closing the app.
func (a *app) unlock(vault string) bool {
	msg := ""
	lockPath := filepath.Join(a.dir, "lockout.json")
	lk := loadLockout(lockPath, readClock())
	for {
		if left := lk.remaining(readClock()); left > 0 {
			a.lockedOut(left)
		}
		code, ok := a.readCode("Enter your unlock code", msg)
		if !ok {
			return false
		}
		var accts []Account
		var err error
		a.wait("Unlocking…", func() { accts, err = loadVault(vault, a.devkey, code) })
		if err == nil {
			if err := resetLockout(lockPath); err != nil {
				log.Printf("lockout: %v", err)
			}
			a.code, a.accts = code, accts
			return true
		}
		if err != ErrWrongCode {
			a.scr.Message("MiniKey", "Can't read the vault: "+err.Error(), ui.Hint{Button: "B", Label: "Exit"})
			waitExit(a.input)
			return false
		}
		lk.fail(readClock())
		if err := lk.save(lockPath); err != nil {
			log.Printf("lockout: %v", err)
		}
		msg = "That code didn't open the vault."
	}
}

// lockedOut counts down a wait earned by wrong codes. It sleeps rather than
// watching the wall clock, so setting the clock doesn't end it.
func (a *app) lockedOut(wait time.Duration) {
	for left := (wait + time.Second - 1).Truncate(time.Second); left > 0; left -= time.Second {
		a.scr.Alert("Wrong code", fmt.Sprintf("Try again in %d s.", int(left.Seconds())))
		time.Sleep(time.Second)
	}
	drain(a.input)
}

// sealTime is about how long opening or sealing the vault takes on the
// device (PBKDF2 with 200000 rounds: deliberately slow, it is what protects
// the codes if the device is stolen).
const sealTime = 4500 * time.Millisecond

// wait runs fn while a ring fills over the expected time, so the slow
// unlock doesn't look like a frozen screen.
func (a *app) wait(msg string, fn func()) {
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	start := time.Now()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		s := a.scr
		s.Background()
		top := s.Header("MiniKey", "")
		cy := float64(top + 130)
		frac := min(0.95, float64(time.Since(start))/float64(sealTime))
		s.Ring(float64(ui.W/2), cy, 40, 6, 1, ui.Line)
		s.Ring(float64(ui.W/2), cy, 40, 6, frac, ui.Text)
		s.TextCenter(ui.W/2, int(cy)+64, msg, ui.Heading, ui.Text)
		s.TextCenter(ui.W/2, int(cy)+96, "This takes a few seconds: it's what keeps your codes safe.", ui.Small, ui.Muted)
		s.Present()
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}

func drain(input <-chan ui.Event) {
	for {
		select {
		case <-input:
		default:
			return
		}
	}
}

// save seals the accounts; it reports false (after telling the user) if it
// couldn't.
func (a *app) save() bool {
	var err error
	a.wait("Saving…", func() { err = saveVault(filepath.Join(a.dir, "vault.enc"), a.devkey, a.code, a.accts) })
	if err != nil {
		a.scr.Notice(a.input, "Couldn't save", err.Error()+". Check that the card has free space.")
		return false
	}
	return true
}

// importNew adds any export it finds and deletes the plain-text file once
// the vault holding its codes is safely written. With asked set, it also
// says when there was nothing to import.
func (a *app) importNew(asked bool) {
	files := findExports(importDirs(a.dir))
	if len(files) == 0 {
		if asked {
			a.scr.Notice(a.input, "Nothing to import", "Copy your export (a .txt file of otpauth:// links, such as Ente Auth's plain-text export) into App/MiniKey or the card's root folder, then choose Import again.")
		}
		return
	}
	var add []Account
	var done []string // exports read in full, safe to delete once sealed
	skipped := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			log.Printf("import %s: %v", f, err)
			continue
		}
		got, sk := ParseExport(string(b))
		add = append(add, got...)
		skipped += sk
		if sk == 0 {
			done = append(done, f)
		}
	}
	merged, n := Merge(a.accts, add)
	old := a.accts
	a.accts = merged
	if !a.save() {
		a.accts = old
		return
	}
	var gone []string
	for _, f := range done {
		if err := shred(f); err != nil {
			log.Printf("delete %s: %v", f, err)
			continue
		}
		gone = append(gone, filepath.Base(f))
	}
	body := fmt.Sprintf("Added %d accounts", n)
	if dup := len(add) - n; dup > 0 {
		body += fmt.Sprintf(" (%d already here)", dup)
	}
	body += "."
	if skipped > 0 {
		body += fmt.Sprintf(" Skipped %d links that aren't time-based codes, and kept the export that has them.", skipped)
	}
	if len(gone) > 0 {
		body += " Deleted the plain-text " + strings.Join(gone, ", ") + "."
	}
	if len(gone) < len(done) {
		body += " Couldn't delete every export: remove it from the card yourself."
	}
	a.scr.Notice(a.input, "Imported", body)
}

// ---- the codes ----

// view is the positions in a.accts of the accounts the list shows: in the
// order of Options › Sort accounts, and only those the search matches.
func (a *app) view() []int { return matching(a.accts, order(a.accts, a.cfg.byName()), a.filter) }

// index is the position in a.accts of the account on row i of the list.
func (a *app) index(i int) int { return a.view()[i] }

func (a *app) items(now time.Time) []ui.Item {
	v := a.view()
	out := make([]ui.Item, len(v))
	for i, j := range v {
		x := a.accts[j]
		sub := ""
		if x.Issuer != "" {
			sub = x.Name
		}
		out[i] = ui.Item{Label: x.Title(), Sub: sub, Detail: Group(x.Code(now))}
	}
	return out
}

func (a *app) run() {
	last := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if len(a.accts) == 0 {
			if !a.empty() {
				return
			}
			continue
		}
		if a.filter != "" && len(a.view()) == 0 {
			a.filter = "" // its last match was removed
		}
		a.drawList()
		select {
		case <-tick.C:
			if time.Since(last) > a.cfg.idle() {
				return
			}
		case ev, ok := <-a.input:
			if !ok {
				return
			}
			if !ev.Pressed {
				continue
			}
			switch ev.Button {
			case ui.A:
				if !a.detail() {
					return
				}
			case ui.Y:
				if !a.settings() {
					return
				}
			case ui.X:
				if !a.search() {
					return
				}
			case ui.L2, ui.R2:
				a.list.Sel = jump(a.titles(), a.list.Sel, ev.Button == ui.R2, a.cfg.byName())
			case ui.Select:
				a.scr.Appearance(a.input)
			case ui.B:
				if a.filter == "" {
					return
				}
				// back to every account, still on the one selected
				j := a.index(a.list.Sel)
				a.filter = ""
				a.list.Sel = slices.Index(a.view(), j)
			case ui.Menu:
				return
			default:
				a.list.Handle(ev)
			}
			last = time.Now() // after the action: a long one isn't idle time
		}
	}
}

func (a *app) drawList() {
	s := a.scr
	now := a.now()
	a.list.Items = a.items(now)
	if a.list.Sel >= len(a.list.Items) {
		a.list.Sel = len(a.list.Items) - 1
	}
	// Accounts can have different periods (30 s, 60 s): the countdown is
	// the selected one's.
	x := a.accts[a.index(max(a.list.Sel, 0))]
	period, left := x.Period, x.Left(now)
	count := fmt.Sprintf("%d accounts", len(a.accts))
	jumpHint, back := "Jump", ui.Hint{Button: "B", Label: "Lock"}
	if a.cfg.byName() {
		jumpHint = "A-Z"
	}
	if a.filter != "" {
		count = fmt.Sprintf("%d of %d match “%s”", len(a.list.Items), len(a.accts), a.filter)
		back.Label = "All"
	}
	top := s.StaticFrame("MiniKey", ui.Fit(ui.Small, count+fmt.Sprintf(" · new code in %d s", left)+a.clockNote(), ui.W-60),
		ui.Hint{Button: "A", Label: "Show"}, ui.Hint{Button: "X", Label: "Search"}, ui.Hint{Button: "Y", Label: "Options"},
		ui.Hint{Button: "L2/R2", Label: jumpHint}, back)
	col := ui.Text
	if left <= 5 {
		col = ui.Warn
	}
	s.Bar(image.Rect(24, top, ui.W-24, top+4), float64(left)/float64(period), col)
	a.list.Draw(s, image.Rect(20, top+12, ui.W-26, ui.H-58))
	s.Present()
}

// detail shows the selected code large, with a countdown ring and the next
// code (unless Options › Next code hides it); UP and DOWN go through the
// other accounts in the list. It reports false when it closed because
// nothing was pressed for the auto-lock time, so the caller locks too.
func (a *app) detail() bool {
	if a.list.Sel < 0 || a.list.Sel >= len(a.view()) {
		return true
	}
	opened := time.Now()
	x := a.accts[a.index(a.list.Sel)]
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		s := a.scr
		now := a.now()
		left := x.Left(now)
		s.Background()
		top := s.Header(x.Title(), x.Name)
		card := image.Rect(60, top+16, ui.W-60, ui.H-70)
		s.Panel(card, 26)
		cx := ui.W / 2
		col := ui.Text
		if left <= 5 {
			col = ui.Warn
		}
		s.Ring(float64(cx), float64(card.Min.Y+70), 42, 6, float64(left)/float64(x.Period), col)
		s.TextCenter(cx, card.Min.Y+70-ui.LineHeight(ui.Heading)/2, fmt.Sprint(left), ui.Heading, col)
		s.TextCenter(cx, card.Min.Y+132, Group(x.Code(now)), ui.Display, ui.Text)
		if !a.cfg.HideNext {
			next := x.Code(now.Add(time.Duration(left) * time.Second))
			s.TextCenter(cx, card.Min.Y+190, "Next  "+Group(next), ui.Body, ui.Muted)
		}
		s.Footer(ui.Hint{Button: "UP/DOWN", Label: "Other accounts"}, ui.Hint{Button: "B", Label: "Back"})
		s.Present()
		select {
		case <-tick.C:
			if time.Since(opened) > a.cfg.idle() {
				return false
			}
		case ev, ok := <-a.input:
			if !ok {
				return false
			}
			if ev.Pressed && (ev.Button == ui.B || ev.Button == ui.Menu || ev.Button == ui.A) {
				return true
			}
			if ev.Pressed && (ev.Button == ui.Up || ev.Button == ui.Down) {
				a.list.Handle(ev)
				x = a.accts[a.index(a.list.Sel)]
			}
			opened = time.Now()
		}
	}
}

// search asks for part of a name and lists only the accounts that have it;
// an empty search lists them all again. It reports false when the keyboard
// was left open longer than the auto-lock time, so the caller locks.
func (a *app) search() bool {
	opened := time.Now()
	q, ok := a.scr.Prompt(a.input, "Search accounts", a.filter, "part of a service or account name", false)
	// The keyboard can't lock itself: whoever closes it after a long wait
	// may not be the owner, so lock rather than show the codes.
	if time.Since(opened) > a.cfg.idle() {
		return false
	}
	if !ok {
		return true
	}
	q = strings.TrimSpace(q)
	if q != "" && len(matching(a.accts, order(a.accts, false), q)) == 0 {
		a.scr.Notice(a.input, "No matches", "No account has “"+q+"” in its name.")
		return true
	}
	a.filter, a.list.Sel = q, 0
	return true
}

// titles are the names of the rows in the list, top to bottom.
func (a *app) titles() []string {
	var out []string
	for _, j := range a.view() {
		out = append(out, a.accts[j].Title())
	}
	return out
}

// empty is shown while the vault has no accounts; false means exit.
func (a *app) empty() bool {
	a.scr.Message("MiniKey", "No codes yet. Copy your export (a .txt file of otpauth:// links) into App/MiniKey or the card's root folder, then press A to import it. MiniKey deletes the plain-text file afterwards.",
		ui.Hint{Button: "A", Label: "Import"}, ui.Hint{Button: "B", Label: "Exit"})
	for ev := range a.input {
		if !ev.Pressed {
			continue
		}
		switch ev.Button {
		case ui.A:
			a.importNew(true)
			return true
		case ui.B, ui.Menu:
			return false
		}
	}
	return false
}

// options is the Y menu's rows, built again after every change.
func (a *app) options() []ui.Item {
	x := a.accts[a.index(a.list.Sel)]
	items := []ui.Item{
		{Label: "Import codes", Sub: "From a .txt export on the card", Chevron: true},
		{Label: "Change unlock code", Chevron: true},
		{Label: "Remove " + x.Title(), Sub: x.Name, Chevron: true},
	}
	items = append(items, settingRows(a.cfg)...)
	return append(items, ui.AppearanceItem())
}

// settings is the Y menu; false means exit: it was left untouched for the
// auto-lock time (after changing the code the vault stays open). The rows
// that change a setting step with A and RIGHT, back with LEFT, and the menu
// stays open; the others close it.
func (a *app) settings() bool {
	l := ui.List{Items: a.options()}
	last := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		top := a.scr.StaticFrame("Options", "", ui.Hint{Button: "A", Label: "Choose"}, ui.Hint{Button: "B", Label: "Back"})
		l.Draw(a.scr, image.Rect(20, top, ui.W-26, ui.H-58))
		a.scr.Present()
		var frame <-chan time.Time
		if ui.NeedsFrame() {
			frame = time.After(ui.FrameDelay)
		}
		var ev ui.Event
		select {
		case <-frame:
			continue
		case <-tick.C:
			if time.Since(last) > a.cfg.idle() {
				return false
			}
			continue
		case e, ok := <-a.input:
			if !ok {
				return true
			}
			ev = e
		}
		if !ev.Pressed {
			continue
		}
		last = time.Now()
		d := 0
		switch ev.Button {
		case ui.A, ui.Right:
			d = 1
		case ui.Left:
			d = -1
		case ui.B, ui.Menu:
			return true
		}
		it := l.Selected()
		if it == nil || d == 0 {
			l.Handle(ev)
			continue
		}
		switch {
		case it.Value != nil:
			old := a.cfg
			a.cfg = a.cfg.change(it.Value, d)
			if err := saveSettings(a.dir, a.cfg); err != nil {
				a.cfg = old
				a.scr.Notice(a.input, "Couldn't save", err.Error()+". Check that the card has free space.")
			}
		case ev.Button != ui.A:
			l.Handle(ev)
			continue
		case it.Label == "Appearance":
			a.scr.Appearance(a.input)
		default:
			a.option(l.Sel)
			return true
		}
		l.Items = a.options()
		last = time.Now() // after the action: a long one isn't idle time
	}
}

// option does one of the action rows in Options.
func (a *app) option(i int) {
	switch i {
	case 0:
		a.importNew(true)
	case 1:
		code, ok := a.newCode("Choose a new unlock code")
		if !ok {
			return
		}
		old := a.code
		a.code = code
		if !a.save() {
			a.code = old
			return
		}
		a.scr.Notice(a.input, "MiniKey", "Unlock code changed.")
	case 2:
		j := a.index(a.list.Sel)
		x := a.accts[j]
		if !a.scr.Confirm(a.input, "Remove "+x.Title()+"?", "Its codes will be gone from this device. Make sure you can still sign in another way.", "Remove") {
			return
		}
		old := a.accts
		a.accts = append(append([]Account(nil), old[:j]...), old[j+1:]...)
		if !a.save() {
			a.accts = old
		}
	}
}
