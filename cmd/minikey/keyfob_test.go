package main

import (
	"encoding/base32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func b32(s string) string {
	return strings.TrimRight(base32.StdEncoding.EncodeToString([]byte(s)), "=")
}

// RFC 6238 appendix B.
func TestRFC6238(t *testing.T) {
	secrets := map[string]string{
		"SHA1":   b32("12345678901234567890"),
		"SHA256": b32("12345678901234567890123456789012"),
		"SHA512": b32("1234567890123456789012345678901234567890123456789012345678901234"),
	}
	cases := []struct {
		t    int64
		algo string
		want string
	}{
		{59, "SHA1", "94287082"}, {59, "SHA256", "46119246"}, {59, "SHA512", "90693936"},
		{1111111109, "SHA1", "07081804"}, {1111111109, "SHA256", "68084774"}, {1111111109, "SHA512", "25091201"},
		{1234567890, "SHA1", "89005924"}, {1234567890, "SHA256", "91819424"}, {1234567890, "SHA512", "93441116"},
		{2000000000, "SHA1", "69279037"}, {2000000000, "SHA256", "90698825"}, {2000000000, "SHA512", "38618901"},
	}
	for _, c := range cases {
		a := Account{Secret: secrets[c.algo], Algo: c.algo, Digits: 8, Period: 30}
		if got := a.Code(time.Unix(c.t, 0)); got != c.want {
			t.Errorf("%s at %d: got %s, want %s", c.algo, c.t, got, c.want)
		}
	}
}

func TestParseExport(t *testing.T) {
	text := "otpauth://totp/GitHub:alice?secret=JBSWY3DPEHPK3PXP&issuer=GitHub\n" +
		"  otpauth://totp/Cloudflare%3Aalice%40example.com?secret=jbsw%20y3dp&algorithm=SHA256&digits=8&period=60\n" +
		"otpauth://hotp/Old?secret=JBSWY3DPEHPK3PXP&counter=1\n" +
		"otpauth://totp/Bad?secret=!!!\n" +
		"not a link\n" +
		"otpauth://totp/Plain?secret=JBSWY3DPEHPK3PXP&codeDisplay=%7B%22pinned%22%3Afalse%7D\n"
	accts, skipped := ParseExport(text)
	if skipped != 2 || len(accts) != 3 {
		t.Fatalf("got %d accounts, %d skipped: %+v", len(accts), skipped, accts)
	}
	if a := accts[0]; a.Issuer != "GitHub" || a.Name != "alice" || a.Algo != "SHA1" || a.Digits != 6 || a.Period != 30 {
		t.Errorf("github: %+v", a)
	}
	if a := accts[1]; a.Issuer != "Cloudflare" || a.Name != "alice@example.com" || a.Secret != "JBSWY3DP" || a.Algo != "SHA256" || a.Digits != 8 || a.Period != 60 {
		t.Errorf("cloudflare: %+v", a)
	}
	if a := accts[2]; a.Title() != "Plain" {
		t.Errorf("plain: %+v", a)
	}
	merged, n := Merge(accts[:1], accts)
	if n != 2 || len(merged) != 3 || merged[0].Title() != "Cloudflare" {
		t.Errorf("merge: n=%d %+v", n, merged)
	}
}

func TestVault(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vault.enc")
	dev := []byte("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	in := []Account{{Issuer: "GitHub", Name: "alice", Secret: "JBSWY3DPEHPK3PXP", Algo: "SHA1", Digits: 6, Period: 30}}
	if err := saveVault(p, dev, "UDLRABXY", in); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "JBSWY3DP") {
		t.Fatal("secret stored in the clear")
	}
	out, err := loadVault(p, dev, "UDLRABXY")
	if err != nil || len(out) != 1 || out[0] != in[0] {
		t.Fatalf("load: %v %+v", err, out)
	}
	if _, err := loadVault(p, dev, "UDLRABXX"); err != ErrWrongCode {
		t.Errorf("wrong code: %v", err)
	}
	if _, err := loadVault(p, []byte("other device"), "UDLRABXY"); err != ErrWrongCode {
		t.Errorf("other device: %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("left behind: %v", ents)
	}
	// A save that fails leaves the old vault and no stray sealed copy.
	bad := filepath.Join(dir, "busy")
	os.MkdirAll(filepath.Join(bad, "inside"), 0755)
	if err := saveVault(bad, dev, "UDLRABXY", in); err == nil {
		t.Fatal("saved over a folder")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 {
		t.Errorf("left behind: %v", ents)
	}
}

func TestFindAndShred(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ente-auth-codes.txt"), []byte("otpauth://totp/A?secret=JBSWY3DP\n"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(dir, ".hidden.txt"), []byte("otpauth://totp/A?secret=JBSWY3DP\n"), 0644)
	got := findExports([]string{dir, filepath.Join(dir, "missing")})
	if len(got) != 1 || filepath.Base(got[0]) != "ente-auth-codes.txt" {
		t.Fatalf("found %v", got)
	}
	if err := shred(got[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got[0]); !os.IsNotExist(err) {
		t.Error("export still there")
	}
}

func TestClockNote(t *testing.T) {
	a := &app{}
	if a.clockNote() != "" || time.Since(a.now()) > time.Second {
		t.Fatal("no skew should mean no note")
	}
	a.skew.Store(int64(-8 * time.Hour)) // device 8 h fast
	if got := a.clockNote(); got != " · clock 8 h fast, corrected" {
		t.Errorf("note: %q", got)
	}
	if d := time.Until(a.now()); d > -8*time.Hour+time.Second || d < -8*time.Hour-time.Second {
		t.Errorf("corrected time off by %v", d)
	}
}

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{40 * time.Second: "40 s", 12 * time.Minute: "12 min", 8 * time.Hour: "8 h", 8*time.Hour + 5*time.Minute: "8 h 5 min"} {
		if got := span(d); got != want {
			t.Errorf("span(%v) = %q, want %q", d, got, want)
		}
	}
}

// Malformed links are refused with an error, never a panic or a half-filled
// account.
func TestParseURIMalformed(t *testing.T) {
	for _, raw := range []string{
		"",
		"otpauth://",
		"otpauth://totp",
		"otpauth://totp/",
		"otpauth://totp/x",
		"otpauth://totp/x?secret=",
		"otpauth://totp/x?secret=%",
		"otpauth://totp/x?secret=1",
		"otpauth://totp/x?secret=A",
		"otpauth://totp/%zz?secret=JBSWY3DP",
		"otpauth://totp/x?secret=JBSWY3DP&digits=0",
		"otpauth://totp/x?secret=JBSWY3DP&digits=9",
		"otpauth://totp/x?secret=JBSWY3DP&digits=six",
		"otpauth://totp/x?secret=JBSWY3DP&period=0",
		"otpauth://totp/x?secret=JBSWY3DP&period=-30",
		"otpauth://totp/x?secret=JBSWY3DP&period=99999999999999999999",
		"otpauth://totp/x?secret=JBSWY3DP&algorithm=MD5",
		"otpauth://hotp/x?secret=JBSWY3DP",
		"otpauth://steam/x?secret=JBSWY3DP",
		"https://totp/x?secret=JBSWY3DP",
		"otpauth:totp/x?secret=JBSWY3DP",
	} {
		if a, err := ParseURI(raw); err == nil {
			t.Errorf("%q: accepted as %+v", raw, a)
		}
	}
}

// Every account ParseURI accepts makes a code of the right length.
func TestParseURICodes(t *testing.T) {
	for _, raw := range []string{
		"otpauth://totp/x?secret=JBSWY3DP",
		"otpauth://totp/a:b:c?secret=jbsw-y3dp-ehpk-3pxp&digits=7&period=1",
		"otpauth://totp/?secret=JBSWY3DPEHPK3PXP====&algorithm=sha512&digits=8&period=300",
	} {
		a, err := ParseURI(raw)
		if err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		if c := a.Code(time.Unix(1700000000, 0)); len(c) != a.Digits || a.Left(time.Now()) < 1 {
			t.Errorf("%q: code %q, %d digits", raw, c, a.Digits)
		}
	}
}
