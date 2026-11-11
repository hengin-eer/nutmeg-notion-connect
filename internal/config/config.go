// Package config finds the key, aliases and cache locations. Nothing here depends
// on the current directory, so nn behaves the same wherever it runs.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/fsutil"
)

const (
	appName = "nutmeg-notion-connect"
	// EnvKey overrides the saved key when set.
	EnvKey = "NUTMEG_NOTION_KEY"
)

func baseDir(xdgVar, fallback string) string {
	if d := os.Getenv(xdgVar); d != "" {
		return filepath.Join(d, appName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, fallback, appName)
}

// ConfigDir holds the key and aliases (per user, never in the repo).
func ConfigDir() string { return baseDir("XDG_CONFIG_HOME", ".config") }

// CacheDir holds the title index and cached pages.
func CacheDir() string { return baseDir("XDG_CACHE_HOME", ".cache") }

func credentialsPath() string { return filepath.Join(ConfigDir(), "credentials") }
func aliasesPath() string     { return filepath.Join(ConfigDir(), "aliases.json") }

// ErrNoKey means neither the env var nor the saved key exists.
var ErrNoKey = errors.New("no key")

// LoadKey returns the key and where it came from ("env" or the file path).
func LoadKey() (key, source string, err error) {
	if k := strings.TrimSpace(os.Getenv(EnvKey)); k != "" {
		return k, "環境変数 " + EnvKey, nil
	}
	b, err := os.ReadFile(credentialsPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", "", ErrNoKey
	}
	if err != nil {
		return "", "", err
	}
	k := strings.TrimSpace(string(b))
	if k == "" {
		return "", "", ErrNoKey
	}
	return k, credentialsPath(), nil
}

// SaveKey stores the key readable only by the current user.
func SaveKey(key string) (string, error) {
	p := credentialsPath()
	return p, fsutil.WriteFileAtomic(p, []byte(strings.TrimSpace(key)+"\n"), 0o600)
}

// DeleteKey removes the saved key. A missing file is not an error.
func DeleteKey() (string, error) {
	p := credentialsPath()
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	return p, nil
}

// Aliases maps short names to page or DB IDs.
type Aliases map[string]string

func LoadAliases() Aliases {
	a := Aliases{}
	b, err := os.ReadFile(aliasesPath())
	if err == nil {
		_ = json.Unmarshal(b, &a)
	}
	return a
}

func (a Aliases) Save() (string, error) {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return "", err
	}
	p := aliasesPath()
	return p, fsutil.WriteFileAtomic(p, b, 0o600)
}

// Names returns alias names in sorted order.
func (a Aliases) Names() []string {
	names := make([]string, 0, len(a))
	for k := range a {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Of returns the alias name for a normalized ID, or "".
func (a Aliases) Of(id string) string {
	for _, k := range a.Names() {
		if n, err := NormID(a[k]); err == nil && n == id {
			return k
		}
	}
	return ""
}

// Resolve turns an alias, URL or bare ID into a hyphenated UUID.
func (a Aliases) Resolve(s string) (string, error) {
	if v, ok := a[s]; ok {
		s = v
	}
	return NormID(s)
}

var idRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{32}`)

// NormID accepts a Notion URL, a 32-hex ID or a hyphenated UUID.
// URLs end with "title-<id>", so the last match wins.
func NormID(s string) (string, error) {
	s, _, _ = strings.Cut(s, "?")
	m := idRe.FindAllString(s, -1)
	if len(m) == 0 {
		return "", fmt.Errorf("IDとして読めません: %s", s)
	}
	h := strings.ToLower(strings.ReplaceAll(m[len(m)-1], "-", ""))
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
