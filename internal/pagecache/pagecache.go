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

type Meta struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Edited     string  `json:"edited"`
	Fetched    float64 `json:"fetched"` // unix seconds
	URL        string  `json:"url,omitempty"`
	PrevEdited string  `json:"prev_edited,omitempty"`
}

type Options struct {
	Links      bool // render URLs (cached separately)
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

func (s *Store) paths(id string, links bool) (md, meta, prev string) {
	suffix := ""
	if links {
		suffix = ".links"
	}
	base := filepath.Join(s.Dir, id+suffix)
	return base + ".md", base + ".json", base + ".prev.md"
}

// Load returns the page markdown, using the cache when the page has not changed.
func (s *Store) Load(id string, opt Options) (string, Meta, error) {
	mdPath, metaPath, prevPath := s.paths(id, opt.Links)
	var old *Meta
	if b, err := os.ReadFile(metaPath); err == nil {
		var m Meta
		if json.Unmarshal(b, &m) == nil {
			old = &m
		}
	}
	cached, cacheErr := os.ReadFile(mdPath)
	hasCache := old != nil && cacheErr == nil
	if opt.CachedOnly && hasCache {
		return string(cached), *old, nil
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
	if hasCache && !opt.Fresh && old.Edited == edited {
		if t, err := time.Parse(time.RFC3339, edited); err == nil {
			fetched := time.Unix(int64(old.Fetched), 0)
			if fetched.Sub(t) > editSettle {
				return string(cached), *old, nil
			}
		}
	}

	r := &render.Renderer{Links: opt.Links, Fetch: func(bid string) ([]notion.Obj, error) {
		var out []notion.Obj
		err := c.Paginate("blocks/"+bid+"/children", nil, func(o notion.Obj) error {
			out = append(out, o)
			return nil
		})
		return out, err
	}}
	body, err := r.Page(id)
	if err != nil {
		return "", Meta{}, err
	}
	meta := Meta{
		ID: id, Title: notion.Title(page), Edited: edited,
		Fetched: float64(time.Now().Unix()), URL: notion.Str(page, "url"),
	}
	if cacheErr == nil && string(cached) != body {
		if err := fsutil.WriteFileAtomic(prevPath, cached, 0o600); err != nil {
			return "", Meta{}, err
		}
		if old != nil {
			meta.PrevEdited = old.Edited
		}
	} else if old != nil {
		meta.PrevEdited = old.PrevEdited
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
	_, _, prevPath := s.paths(id, false)
	b, err := os.ReadFile(prevPath)
	if err != nil {
		return "", false
	}
	return string(b), true
}
