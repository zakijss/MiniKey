package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Account is one 2FA entry, as read from an otpauth:// URI.
type Account struct {
	Issuer string `json:"issuer,omitempty"`
	Name   string `json:"name,omitempty"`
	Secret string `json:"secret"` // base32, upper case, no padding or spaces
	Algo   string `json:"algo"`   // SHA1, SHA256 or SHA512
	Digits int    `json:"digits"`
	Period int    `json:"period"`
}

// Title is what the list shows first: the issuer, or the account name.
func (a Account) Title() string {
	if a.Issuer != "" {
		return a.Issuer
	}
	return a.Name
}

func (a Account) key() string { return a.Issuer + "\x00" + a.Name + "\x00" + a.Secret }

func decodeSecret(s string) ([]byte, error) {
	s = strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(s))
	if s == "" {
		return nil, errors.New("empty secret")
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
}

// ParseURI reads one otpauth://totp/ URI. Other kinds (hotp, steam) are
// refused with an error that says so.
func ParseURI(raw string) (Account, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "otpauth" {
		return Account{}, errors.New("not an otpauth:// link")
	}
	if !strings.EqualFold(u.Host, "totp") {
		return Account{}, fmt.Errorf("%s codes aren't supported, only time-based (totp)", u.Host)
	}
	q := u.Query()
	a := Account{Algo: "SHA1", Digits: 6, Period: 30}
	label := strings.TrimPrefix(u.Path, "/")
	if i := strings.Index(label, ":"); i >= 0 {
		a.Issuer, a.Name = strings.TrimSpace(label[:i]), strings.TrimSpace(label[i+1:])
	} else {
		a.Name = strings.TrimSpace(label)
	}
	if v := q.Get("issuer"); v != "" {
		a.Issuer = v
	}
	sec, err := decodeSecret(q.Get("secret"))
	if err != nil || len(sec) == 0 {
		return Account{}, errors.New("the secret isn't valid base32")
	}
	a.Secret = strings.TrimRight(base32.StdEncoding.EncodeToString(sec), "=")
	if v := q.Get("algorithm"); v != "" {
		switch strings.ToUpper(v) {
		case "SHA1", "SHA256", "SHA512":
			a.Algo = strings.ToUpper(v)
		default:
			return Account{}, fmt.Errorf("unknown algorithm %q", v)
		}
	}
	if v := q.Get("digits"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 6 || n > 8 {
			return Account{}, fmt.Errorf("unsupported digits %q", v)
		}
		a.Digits = n
	}
	if v := q.Get("period"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 300 {
			return Account{}, fmt.Errorf("unsupported period %q", v)
		}
		a.Period = n
	}
	return a, nil
}

// ParseExport reads every otpauth:// line of a plain-text export (Ente
// Auth, Aegis, 2FAS and others write one URI per line). It returns the
// accounts and how many links it had to skip.
func ParseExport(text string) (accts []Account, skipped int) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, "otpauth://")
		if i < 0 {
			continue
		}
		a, err := ParseURI(line[i:])
		if err != nil {
			skipped++
			continue
		}
		accts = append(accts, a)
	}
	return accts, skipped
}

// Merge adds new accounts to old, skipping exact duplicates, sorted by
// title. It returns the result and how many were added.
func Merge(old, add []Account) ([]Account, int) {
	seen := map[string]bool{}
	out := append([]Account(nil), old...)
	for _, a := range old {
		seen[a.key()] = true
	}
	n := 0
	for _, a := range add {
		if !seen[a.key()] {
			seen[a.key()] = true
			out = append(out, a)
			n++
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title()+out[i].Name) < strings.ToLower(out[j].Title()+out[j].Name)
	})
	return out, n
}

// Code is the RFC 6238 code for t.
func (a Account) Code(t time.Time) string {
	key, err := decodeSecret(a.Secret)
	if err != nil {
		return "------"
	}
	var h func() hash.Hash
	switch a.Algo {
	case "SHA256":
		h = sha256.New
	case "SHA512":
		h = sha512.New
	default:
		h = sha1.New
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(t.Unix())/uint64(a.Period))
	m := hmac.New(h, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < a.Digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", a.Digits, v%mod)
}

// Left is how many seconds the code shown at t stays valid.
func (a Account) Left(t time.Time) int {
	return a.Period - int(t.Unix()%int64(a.Period))
}

// Group splits a code for reading: 123 456, 1234 5678.
func Group(code string) string {
	switch len(code) {
	case 6:
		return code[:3] + " " + code[3:]
	case 7:
		return code[:3] + " " + code[3:]
	case 8:
		return code[:4] + " " + code[4:]
	}
	return code
}
