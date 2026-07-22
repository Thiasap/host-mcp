package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type openedRoot struct {
	Config RootConfig
	Root   *os.Root
}
type FileSystem struct {
	roots  map[string]openedRoot
	config Config
	audit  Auditor
}
type RootInfo struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Kind        string `json:"kind,omitempty"`
	Read        bool   `json:"read"`
	Write       bool   `json:"write"`
	Delete      bool   `json:"delete"`
	ExecCWD     bool   `json:"exec_cwd"`
	ReadOnly    bool   `json:"read_only"`
}
type RootsInput struct{}
type RootsOutput struct {
	Roots []RootInfo `json:"roots"`
}
type StatInput struct {
	Root string `json:"root"`
	Path string `json:"path"`
}
type StatOutput struct {
	Root     string `json:"root"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	Modified string `json:"modified"`
	ReadOnly bool   `json:"read_only"`
}
type ListInput struct {
	Root  string `json:"root"`
	Path  string `json:"path"`
	Limit int    `json:"limit,omitempty"`
}
type ListEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	Modified string `json:"modified"`
}
type ListOutput struct {
	Root      string      `json:"root"`
	Path      string      `json:"path"`
	Entries   []ListEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}
type ReadInput struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int64  `json:"limit,omitempty"`
}
type ReadOutput struct {
	Root      string `json:"root"`
	Path      string `json:"path"`
	Encoding  string `json:"encoding"`
	Data      string `json:"data"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
}
type SearchInput struct {
	Root          string `json:"root"`
	Path          string `json:"path"`
	Pattern       string `json:"pattern"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
}
type SearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}
type SearchOutput struct {
	Root         string        `json:"root"`
	Matches      []SearchMatch `json:"matches"`
	Truncated    bool          `json:"truncated"`
	FilesScanned int           `json:"files_scanned"`
}
type WriteInput struct {
	Root           string `json:"root"`
	Path           string `json:"path"`
	Data           string `json:"data"`
	Encoding       string `json:"encoding,omitempty"`
	Overwrite      bool   `json:"overwrite,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
}
type WriteOutput struct {
	Root  string `json:"root"`
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}
type MkdirInput struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	Parents bool   `json:"parents,omitempty"`
}
type PathOutput struct {
	Root string `json:"root"`
	Path string `json:"path"`
}
type RenameInput struct {
	SourceRoot      string `json:"source_root"`
	SourcePath      string `json:"source_path"`
	DestinationRoot string `json:"destination_root"`
	DestinationPath string `json:"destination_path"`
	Overwrite       bool   `json:"overwrite,omitempty"`
}
type RenameOutput struct {
	SourceRoot      string `json:"source_root"`
	SourcePath      string `json:"source_path"`
	DestinationRoot string `json:"destination_root"`
	DestinationPath string `json:"destination_path"`
}
type DeleteInput struct {
	Root           string `json:"root"`
	Path           string `json:"path"`
	Recursive      bool   `json:"recursive,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
}

func OpenFileSystem(c Config, a Auditor) (*FileSystem, error) {
	f := &FileSystem{roots: map[string]openedRoot{}, config: c, audit: a}
	for _, rc := range c.Roots {
		if err := verifyProfileRoot(c.Profile, rc); err != nil {
			f.Close()
			return nil, fmt.Errorf("verify root %q: %w", rc.ID, err)
		}
		r, err := os.OpenRoot(rc.Path)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("open root %q: %w", rc.ID, err)
		}
		f.roots[rc.ID] = openedRoot{rc, r}
	}
	return f, nil
}
func verifyProfileRoot(profile Profile, rc RootConfig) error {
	if profile != ProfileTermux {
		return profileRootAllowed(profile, rc.Path)
	}
	prefix := os.Getenv("PREFIX")
	if prefix == "" {
		prefix = "/data/data/com.termux/files/usr"
	}
	switch rc.Kind {
	case "termux-prefix":
		if filepath.Clean(rc.Path) != filepath.Clean(prefix) {
			return errors.New("termux-prefix does not match effective PREFIX")
		}
	case "termux-home":
		home, err := filepath.Abs(os.Getenv("HOME"))
		if err == nil && os.Getenv("HOME") != "" && filepath.Clean(rc.Path) != filepath.Clean(home) {
			return errors.New("termux-home does not match effective HOME")
		}
	case "termux-storage":
		if !rc.ReadOnly {
			return errors.New("verified storage must be read-only")
		}
		home := os.Getenv("HOME")
		if home == "" {
			return errors.New("HOME is required to verify storage")
		}
		entries, _ := os.ReadDir(filepath.Join(home, "storage"))
		for _, e := range entries {
			target, err := filepath.EvalSymlinks(filepath.Join(home, "storage", e.Name()))
			if err == nil && filepath.Clean(target) == filepath.Clean(rc.Path) {
				return nil
			}
		}
		return errors.New("path is not a verified ~/storage target")
	}
	return nil
}

func (f *FileSystem) Close() error {
	var first error
	for _, r := range f.roots {
		if err := r.Root.Close(); first == nil && err != nil {
			first = err
		}
	}
	return first
}
func cleanPath(p string) (string, error) {
	if p == "" {
		p = "."
	}
	if strings.IndexByte(p, 0) >= 0 || filepath.IsAbs(p) {
		return "", errors.New("path must be relative")
	}
	p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", errors.New("path escapes root")
	}
	return p, nil
}

type resolved struct {
	id, path string
	cfg      RootConfig
	root     *os.Root
}

func (f *FileSystem) resolve(id, path, op string) (resolved, error) {
	r, ok := f.roots[id]
	if !ok {
		return resolved{}, fmt.Errorf("unknown root %q", id)
	}
	p, err := cleanPath(path)
	if err != nil {
		return resolved{}, err
	}
	if !grantAllows(f.config, id, op, p) {
		return resolved{}, fmt.Errorf("%s permission denied for root %q path %q", op, id, p)
	}
	if op != "read" && (r.Config.ReadOnly || !r.Config.WriteEligible) {
		return resolved{}, fmt.Errorf("root %q is read-only", id)
	}
	return resolved{id, p, r.Config, r.Root}, nil
}
func (f *FileSystem) Roots(context.Context, RootsInput) (RootsOutput, error) {
	out := RootsOutput{}
	for _, r := range f.config.Roots {
		execCWD := false
		for _, rule := range f.config.ExecRules {
			for _, cwd := range rule.CWDs {
				if cwd.Root == r.ID {
					execCWD = true
				}
			}
		}
		out.Roots = append(out.Roots, RootInfo{r.ID, r.Description, r.Kind, hasRootGrant(f.config, r.ID, "read"), hasRootGrant(f.config, r.ID, "write") && !r.ReadOnly, hasRootGrant(f.config, r.ID, "delete") && !r.ReadOnly, execCWD, r.ReadOnly})
	}
	return out, nil
}
func hasRootGrant(c Config, root, operation string) bool {
	for _, g := range c.Permissions {
		if g.Root == root && g.Operation == operation {
			return true
		}
	}
	return false
}
func fileType(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "directory"
	case m.IsRegular():
		return "file"
	case m&fs.ModeSymlink != 0:
		return "symlink"
	default:
		return "other"
	}
}
func (f *FileSystem) Stat(_ context.Context, in StatInput) (StatOutput, error) {
	r, e := f.resolve(in.Root, in.Path, "read")
	if e != nil {
		return StatOutput{}, e
	}
	st, e := r.root.Stat(r.path)
	if e != nil {
		return StatOutput{}, e
	}
	return StatOutput{r.id, r.path, fileType(st.Mode()), st.Size(), st.Mode().String(), st.ModTime().UTC().Format(time.RFC3339), r.cfg.ReadOnly || !grantAllows(f.config, r.id, "write", r.path)}, nil
}
func (f *FileSystem) List(_ context.Context, in ListInput) (ListOutput, error) {
	r, e := f.resolve(in.Root, in.Path, "read")
	if e != nil {
		return ListOutput{}, e
	}
	limit := in.Limit
	if limit <= 0 || limit > f.config.Limits.MaxListEntries {
		limit = f.config.Limits.MaxListEntries
	}
	d, e := r.root.Open(r.path)
	if e != nil {
		return ListOutput{}, e
	}
	defer d.Close()
	entries, e := d.ReadDir(limit + 1)
	if e != nil && e != io.EOF {
		return ListOutput{}, e
	}
	out := ListOutput{Root: r.id, Path: r.path, Truncated: len(entries) > limit}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	for _, x := range entries {
		if st, e := x.Info(); e == nil {
			out.Entries = append(out.Entries, ListEntry{x.Name(), fileType(st.Mode()), st.Size(), st.Mode().String(), st.ModTime().UTC().Format(time.RFC3339)})
		}
	}
	slices.SortFunc(out.Entries, func(a, b ListEntry) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}
func (f *FileSystem) Read(_ context.Context, in ReadInput) (ReadOutput, error) {
	r, e := f.resolve(in.Root, in.Path, "read")
	if e != nil {
		return ReadOutput{}, e
	}
	if in.Offset < 0 {
		return ReadOutput{}, errors.New("offset must be non-negative")
	}
	limit := in.Limit
	if limit <= 0 || limit > f.config.Limits.MaxReadBytes {
		limit = f.config.Limits.MaxReadBytes
	}
	file, e := r.root.Open(r.path)
	if e != nil {
		return ReadOutput{}, e
	}
	defer file.Close()
	if in.Offset > 0 {
		if _, e = file.Seek(in.Offset, io.SeekStart); e != nil {
			return ReadOutput{}, e
		}
	}
	b, e := io.ReadAll(io.LimitReader(file, limit+1))
	if e != nil {
		return ReadOutput{}, e
	}
	tr := int64(len(b)) > limit
	if tr {
		b = b[:limit]
	}
	enc, data := "utf-8", string(b)
	if !bytes.Equal([]byte(data), b) || bytes.ContainsRune(b, '\x00') {
		enc = "base64"
		data = base64.StdEncoding.EncodeToString(b)
	}
	return ReadOutput{r.id, r.path, enc, data, len(b), tr}, nil
}
func (f *FileSystem) Search(ctx context.Context, in SearchInput) (SearchOutput, error) {
	r, e := f.resolve(in.Root, in.Path, "read")
	if e != nil {
		return SearchOutput{}, e
	}
	if in.Pattern == "" {
		return SearchOutput{}, errors.New("pattern is required")
	}
	max := in.MaxResults
	if max <= 0 || max > f.config.Limits.MaxSearchResults {
		max = f.config.Limits.MaxSearchResults
	}
	needle := in.Pattern
	var re *regexp.Regexp
	if in.Regex {
		pat := needle
		if !in.CaseSensitive {
			pat = "(?i)" + pat
		}
		re, e = regexp.Compile(pat)
		if e != nil {
			return SearchOutput{}, e
		}
	} else if !in.CaseSensitive {
		needle = strings.ToLower(needle)
	}
	out := SearchOutput{Root: r.id}
	e = fs.WalkDir(r.root.FS(), r.path, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			return nil
		}
		st, e := d.Info()
		if e != nil || !st.Mode().IsRegular() || st.Size() > f.config.Limits.MaxSearchFileSize {
			return nil
		}
		file, e := r.root.Open(path)
		if e != nil {
			return nil
		}
		defer file.Close()
		out.FilesScanned++
		sc := bufio.NewScanner(file)
		sc.Buffer(make([]byte, 64*1024), int(f.config.Limits.MaxSearchFileSize))
		line := 0
		for sc.Scan() {
			line++
			text := sc.Text()
			hay := text
			if !in.CaseSensitive && !in.Regex {
				hay = strings.ToLower(hay)
			}
			match := strings.Contains(hay, needle)
			if re != nil {
				match = re.MatchString(text)
			}
			if match {
				out.Matches = append(out.Matches, SearchMatch{filepath.ToSlash(path), line, capString(text, 4096)})
				if len(out.Matches) >= max {
					out.Truncated = true
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	return out, e
}
func capString(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
func expectedSHA256(s string) ([]byte, error) {
	if len(s) != sha256.Size*2 {
		return nil, errors.New("expected_sha256 must be a lowercase SHA-256 hex digest")
	}
	b, e := hex.DecodeString(s)
	if e != nil || hex.EncodeToString(b) != s {
		return nil, errors.New("expected_sha256 must be a lowercase SHA-256 hex digest")
	}
	return b, nil
}
func existingRegularHash(root *os.Root, path, expected string) (bool, error) {
	st, e := root.Lstat(path)
	if errors.Is(e, fs.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if !st.Mode().IsRegular() {
		return false, errors.New("existing path is not a regular file")
	}
	want, e := expectedSHA256(expected)
	if e != nil {
		return false, e
	}
	file, e := root.Open(path)
	if e != nil {
		return false, e
	}
	defer file.Close()
	h := sha256.New()
	if _, e = io.Copy(h, file); e != nil {
		return false, e
	}
	if !bytes.Equal(h.Sum(nil), want) {
		return false, errors.New("expected_sha256 does not match existing file")
	}
	return true, nil
}
func (f *FileSystem) Write(_ context.Context, in WriteInput) (WriteOutput, error) {
	r, e := f.resolve(in.Root, in.Path, "write")
	if e != nil {
		return WriteOutput{}, e
	}
	var b []byte
	if in.Encoding == "base64" {
		b, e = base64.StdEncoding.DecodeString(in.Data)
	} else if in.Encoding == "" || in.Encoding == "utf-8" {
		b = []byte(in.Data)
	} else {
		return WriteOutput{}, errors.New("encoding must be utf-8 or base64")
	}
	if e != nil {
		return WriteOutput{}, e
	}
	if int64(len(b)) > f.config.Limits.MaxWriteBytes {
		return WriteOutput{}, errors.New("write exceeds configured limit")
	}
	exists, e := existingRegularHash(r.root, r.path, in.ExpectedSHA256)
	if e != nil {
		return WriteOutput{}, e
	}
	if exists && !in.Overwrite {
		return WriteOutput{}, errors.New("destination exists; set overwrite")
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !in.Overwrite {
		flags |= os.O_EXCL
	}
	file, e := r.root.OpenFile(r.path, flags, 0600)
	if e != nil {
		return WriteOutput{}, e
	}
	if _, e = file.Write(b); e == nil {
		e = file.Sync()
	}
	ce := file.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return WriteOutput{}, e
	}
	f.audit.Log("fs_write", "ok", r.id+":"+r.path, fmt.Sprintf("bytes=%d", len(b)))
	return WriteOutput{r.id, r.path, len(b)}, nil
}
func (f *FileSystem) Mkdir(_ context.Context, in MkdirInput) (PathOutput, error) {
	r, e := f.resolve(in.Root, in.Path, "write")
	if e != nil {
		return PathOutput{}, e
	}
	if in.Parents {
		e = r.root.MkdirAll(r.path, 0700)
	} else {
		e = r.root.Mkdir(r.path, 0700)
	}
	if e == nil {
		f.audit.Log("fs_mkdir", "ok", r.id+":"+r.path, "")
	}
	return PathOutput{r.id, r.path}, e
}
func (f *FileSystem) Rename(_ context.Context, in RenameInput) (RenameOutput, error) {
	if in.SourceRoot != in.DestinationRoot {
		return RenameOutput{}, errors.New("cross-root rename is not supported")
	}
	from, e := f.resolve(in.SourceRoot, in.SourcePath, "write")
	if e != nil {
		return RenameOutput{}, e
	}
	to, e := f.resolve(in.DestinationRoot, in.DestinationPath, "write")
	if e != nil {
		return RenameOutput{}, e
	}
	if !in.Overwrite {
		if _, e := to.root.Lstat(to.path); e == nil {
			return RenameOutput{}, errors.New("destination exists")
		} else if !errors.Is(e, fs.ErrNotExist) {
			return RenameOutput{}, e
		}
	}
	if e = from.root.Rename(from.path, to.path); e != nil {
		return RenameOutput{}, e
	}
	f.audit.Log("fs_rename", "ok", from.id+":"+from.path, to.path)
	return RenameOutput{from.id, from.path, to.id, to.path}, nil
}
func (f *FileSystem) Delete(_ context.Context, in DeleteInput) (PathOutput, error) {
	if in.Recursive {
		return PathOutput{}, errors.New("recursive delete is not supported")
	}
	r, e := f.resolve(in.Root, in.Path, "delete")
	if e != nil {
		return PathOutput{}, e
	}
	st, e := r.root.Lstat(r.path)
	if e != nil {
		return PathOutput{}, e
	}
	if st.Mode().IsRegular() {
		if _, e = existingRegularHash(r.root, r.path, in.ExpectedSHA256); e != nil {
			return PathOutput{}, e
		}
	} else if st.Mode()&fs.ModeSymlink != 0 {
		return PathOutput{}, errors.New("refusing to delete symlink")
	} else if !st.IsDir() {
		return PathOutput{}, errors.New("refusing to delete non-regular file")
	}
	if e = r.root.Remove(r.path); e != nil {
		return PathOutput{}, e
	}
	f.audit.Log("fs_delete", "ok", r.id+":"+r.path, "recursive=false")
	return PathOutput{r.id, r.path}, nil
}
