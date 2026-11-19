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
	// Unseen marks a copy fetched ahead of time and not yet read. The previous
	// version stays the last one that was read, so diff still means
	// "since you last read it".
	Unseen bool `json:"unseen,omitempty"`
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

func (s *Store) cached(id string) (old *Meta, cached []byte, ok bool) {
	mdPath, metaPath, _ := s.paths(id)
	if b, err := os.ReadFile(metaPath); err == nil {
		var m Meta
		if json.Unmarshal(b, &m) == nil && m.Format == formatVersion {
			old = &m
		}
	}
	cached, err := os.ReadFile(mdPath)
	return old, cached, old != nil && err == nil
}

// current reports whether a cached copy is still the page edited at edited.
func current(old *Meta, edited string) bool {
	if old.Edited != edited || old.Omitted != 0 {
		return false
	}
	t, err := time.Parse(time.RFC3339, edited)
	return err == nil && time.Unix(int64(old.Fetched), 0).Sub(t) > editSettle
}

// seen returns a cached copy as read, clearing the prefetch mark.
func (s *Store) seen(id string, old *Meta, cached []byte, links bool) (string, Meta, error) {
	if old.Unseen {
		old.Unseen = false
		_, metaPath, _ := s.paths(id)
		mb, _ := json.Marshal(old)
		if err := fsutil.WriteFileAtomic(metaPath, mb, 0o600); err != nil {
			return "", Meta{}, err
		}
	}
	return display(string(cached), links), *old, nil
}

// Load returns the page markdown, using the cache when the page has not changed.
func (s *Store) Load(id string, opt Options) (string, Meta, error) {
	old, cached, hasCache := s.cached(id)
	if opt.CachedOnly && hasCache {
		return s.seen(id, old, cached, opt.Links)
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
	if hasCache && !opt.Fresh && current(old, edited) {
		return s.seen(id, old, cached, opt.Links)
	}
	meta := Meta{Title: notion.Title(page), Edited: edited, URL: notion.Str(page, "url")}
	body, meta, err := s.fetch(c, id, meta, old, cached, hasCache)
	if err != nil {
		return "", Meta{}, err
	}
	return display(body, opt.Links), meta, nil
}

// Prefetch caches a page ahead of time, using the title and edit time from the
// index so that no page lookup is needed. It reports whether it fetched.
func (s *Store) Prefetch(id, title, edited string) (bool, error) {
	old, cached, hasCache := s.cached(id)
	if hasCache && current(old, edited) {
		return false, nil
	}
	meta := Meta{Title: title, Edited: edited, Unseen: true}
	if old != nil {
		meta.URL = old.URL
	}
	_, _, err := s.fetch(s.Client(), id, meta, old, cached, hasCache)
	return err == nil, err
}

// fetch gets the body and saves it with meta (Title, Edited, URL, Unseen set).
func (s *Store) fetch(c *notion.Client, id string, meta Meta, old *Meta, cached []byte, hasCache bool) (string, Meta, error) {
	mdPath, metaPath, prevPath := s.paths(id)
	content, err := render.Markdown(c.Get, id)
	if err != nil {
		return "", Meta{}, err
	}
	body := render.Squash(content.Markdown, true)
	meta.Format, meta.ID = formatVersion, id
	meta.Fetched = float64(time.Now().Unix())
	meta.Omitted = content.Omitted
	// The previous version is the last one read. A partial copy is not a real
	// version (diff would show the missing parts as deletions), and an unread
	// prefetched copy is not what the reader saw.
	if hasCache && string(cached) != body && old.Omitted == 0 && meta.Omitted == 0 && !old.Unseen {
		if err := fsutil.WriteFileAtomic(prevPath, cached, 0o600); err != nil {
			return "", Meta{}, err
		}
		meta.PrevEdited = old.Edited
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
	return body, meta, nil
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
