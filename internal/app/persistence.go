package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := os.Chmod(path, 0700); err != nil {
		return err
	}
	return nil
}

func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if d, err := os.Open(filepath.Dir(path)); err == nil {
			_ = d.Sync()
			_ = d.Close()
		}
	}
	ok = true
	return nil
}

func SaveConfig(path string, c Config) error {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return AtomicWrite(path, b, 0600)
}

func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func SaveToken(path, token string) error {
	if token == "" {
		return errors.New("empty token")
	}
	return AtomicWrite(path, []byte(token+"\n"), 0600)
}
func LoadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	t := string(b)
	for len(t) > 0 && (t[len(t)-1] == '\n' || t[len(t)-1] == '\r') {
		t = t[:len(t)-1]
	}
	if t == "" {
		return "", errors.New("empty token file")
	}
	return t, nil
}
func TokenEqual(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}

func CheckPrivate(path string, wantDir bool) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() != wantDir {
		return fmt.Errorf("unexpected file type")
	}
	if st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("permissions %04o are too broad", st.Mode().Perm())
	}
	return nil
}

type AuditEvent struct {
	Time    string `json:"time"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	Subject string `json:"subject,omitempty"`
	Detail  string `json:"detail,omitempty"`
}
type Auditor struct {
	Path     string
	MaxBytes int64
	Keep     int
}

func (a Auditor) Log(action, outcome, subject, detail string) {
	_ = a.log(action, outcome, subject, detail)
}
func (a Auditor) log(action, outcome, subject, detail string) error {
	if a.MaxBytes <= 0 {
		a.MaxBytes = 2 << 20
	}
	if a.Keep <= 0 {
		a.Keep = 3
	}
	if err := EnsurePrivateDir(filepath.Dir(a.Path)); err != nil {
		return err
	}
	if st, err := os.Stat(a.Path); err == nil && st.Size() >= a.MaxBytes {
		for i := a.Keep - 1; i >= 1; i-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", a.Path, i), fmt.Sprintf("%s.%d", a.Path, i+1))
		}
		_ = os.Rename(a.Path, a.Path+".1")
	}
	f, err := os.OpenFile(a.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_ = f.Chmod(0600)
	return json.NewEncoder(f).Encode(AuditEvent{time.Now().UTC().Format(time.RFC3339Nano), action, outcome, subject, detail})
}

func CopyCapped(dst io.Writer, src io.Reader, limit int64) (int64, bool, error) {
	lr := io.LimitReader(src, limit+1)
	n, err := io.Copy(dst, lr)
	return n, n > limit, err
}
