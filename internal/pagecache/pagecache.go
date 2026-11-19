// Package pagecache stores rendered pages keyed by last_edited_time, and keeps
// the previous version so nn diff can show what changed.
package pagecache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/fsutil"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/render"
)

// last_edited_time has minute precision, so a fetch within this window after an
// edit may have missed later edits in the same minute; such entries are refetched.
const editSettle = 90 * time.Second

const formatVersion = 2

type Meta struct {
	Format     int     `json:"format"`
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Edited     string  `json:"edited"`
	Fetched    float64 `json:"fetched"` // unix seconds
	URL        string  `json:"url,omitempty"`
	PrevEdited string  `json:"prev_edited,omitempty"`
	// Omitted counts truncated parts that could not be fetched. Such a page is
	// shown with a warning and fetched again on the next read.
	Omitted int `json:"omitted,omitempty"`
}

type Options struct {
	Links      bool // include URLs in output (one shared cache)
	Fresh      bool // ignore the cache
	CachedOnly bool // no API calls when a cached copy exists
}

// IsDatabaseError means the ID was a database, not a page.
type IsDatabaseError struct{ ID, Title string }

func (e *IsDatabaseError) Error() string {
	return fmt.Sprintf("これはDBです（%s）。`nn db %s` で見てください", e.Title, e.ID)
}

type Store struct {
	Dir    string
	Client func() *notion.Client // created lazily so --cached never needs a key
}

func (s *Store) paths(id string) (md, meta, prev string) {
	base := filepath.Join(s.Dir, id)
	return base + ".md", base + ".json", base + ".prev.md"
}

func display(body string, links bool) string {
	if links {
		return body
	}
	return render.WithoutLinks(body)
}

// Load returns the page markdown, using the cache when the page has not changed.
func (s *Store) Load(id string, opt Options) (string, Meta, error) {
	mdPath, metaPath, prevPath := s.paths(id)
	var old *Meta
	if b, err := os.ReadFile(metaPath); err == nil {
		var m Meta
		if json.Unmarshal(b, &m) == nil && m.Format == formatVersion {
			old = &m
		}
	}
	cached, cacheErr := os.ReadFile(mdPath)
	hasCache := old != nil && cacheErr == nil
	if opt.CachedOnly && hasCache {
		return display(string(cached), opt.Links), *old, nil
	}

	c := s.Client()
	page, err := c.Get("pages/" + id)
	if err != nil {
		if notion.IsStatus(err, 400, 404) {
			if d, derr := c.Get("databases/" + id); derr == nil {
				return "", Meta{}, &IsDatabaseError{ID: id, Title: notion.Title(d)}
			}
		}
		return "", Meta{}, err
	}
	edited := notion.Str(page, "last_edited_time")
	if hasCache && !opt.Fresh && old.Edited == edited && old.Omitted == 0 {
		if t, err := time.Parse(time.RFC3339, edited); err == nil {
			fetched := time.Unix(int64(old.Fetched), 0)
			if fetched.Sub(t) > editSettle {
				return display(string(cached), opt.Links), *old, nil
			}
		}
	}

	content, err := render.Markdown(c.Get, id)
	if err != nil {
		return "", Meta{}, err
	}
	body := render.Squash(content.Markdown, true)
	meta := Meta{
		Format: formatVersion, ID: id, Title: notion.Title(page), Edited: edited,
		Fetched: float64(time.Now().Unix()), URL: notion.Str(page, "url"),
		Omitted: content.Omitted,
	}
	// A partial copy is not a real version: comparing with it would make diff
	// report the missing parts as deletions, so it never becomes the previous one.
	if hasCache && string(cached) != body && old.Omitted == 0 && meta.Omitted == 0 {
		if err := fsutil.WriteFileAtomic(prevPath, cached, 0o600); err != nil {
			return "", Meta{}, err
		}
		if old != nil {
			meta.PrevEdited = old.Edited
		}
	} else if old != nil {
		meta.PrevEdited = old.PrevEdited
	}
	if !hasCache {
		if err := os.Remove(prevPath); err != nil && !os.IsNotExist(err) {
			return "", Meta{}, err
		}
	}
	if err := fsutil.WriteFileAtomic(mdPath, []byte(body), 0o600); err != nil {
		return "", Meta{}, err
	}
	mb, _ := json.Marshal(meta)
	if err := fsutil.WriteFileAtomic(metaPath, mb, 0o600); err != nil {
		return "", Meta{}, err
	}
	return display(body, opt.Links), meta, nil
}

// Prev returns the previous version saved when the page last changed.
func (s *Store) Prev(id string) (string, bool) {
	_, _, prevPath := s.paths(id)
	b, err := os.ReadFile(prevPath)
	if err != nil {
		return "", false
	}
	return render.WithoutLinks(string(b)), true
}
