package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zakijss/minideck/atomicfile"
	"github.com/zakijss/minideck/secrets"
)

// The vault is a JSON list of accounts sealed with secrets.Encrypt (OpenSSL
// AES-256-CBC, PBKDF2 200k). The password is the device key from internal
// flash plus the unlock code typed on the buttons, so the card alone opens
// nothing and the device alone needs the code.

const magic = "KEYFOB-V1"

type vaultFile struct {
	Magic    string    `json:"magic"`
	Accounts []Account `json:"accounts"`
}

// ErrWrongCode means the vault didn't open with the code given.
var ErrWrongCode = errors.New("wrong unlock code")

func pass(devkey []byte, code string) []byte {
	return []byte(string(devkey) + "\x00keyfob\x00" + code)
}

func loadVault(path string, devkey []byte, code string) ([]Account, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pt, err := secrets.Decrypt(blob, pass(devkey, code))
	if err != nil {
		return nil, ErrWrongCode
	}
	var v vaultFile
	if json.Unmarshal(pt, &v) != nil || v.Magic != magic {
		return nil, ErrWrongCode
	}
	return v.Accounts, nil
}

// saveVault writes atomically and reads the file back before replacing the
// old one, so a bad write can't lose the codes.
func saveVault(path string, devkey []byte, code string, accts []Account) error {
	pt, err := json.Marshal(vaultFile{Magic: magic, Accounts: accts})
	if err != nil {
		return err
	}
	blob, err := secrets.Encrypt(pt, pass(devkey, code))
	if err != nil {
		return err
	}
	err = atomicfile.WriteChecked(path, blob, 0600)
	if errors.Is(err, atomicfile.ErrReadBack) {
		return errors.New("the card didn't store the vault correctly")
	}
	return err
}

// findExports lists plain-text files in dirs that contain otpauth:// links.
func findExports(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".txt") {
				continue
			}
			p := filepath.Join(d, name)
			if fi, err := e.Info(); err != nil || fi.Size() > 1<<20 {
				continue
			}
			if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), "otpauth://") {
				out = append(out, p)
			}
		}
	}
	return out
}

// shred overwrites a file with zeros before removing it. On an SD card this
// is best effort (the controller may keep old blocks), but it stops the
// plain text from being read back through the file system.
func shred(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err == nil {
		f.Write(make([]byte, fi.Size()))
		f.Sync()
		f.Close()
	}
	return os.Remove(path)
}
