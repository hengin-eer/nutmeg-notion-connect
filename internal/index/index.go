// Package index keeps a local list of every page and DB title, so finding
// things costs no API calls.
package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/fsutil"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

const (
	// An incremental sync rereads a little before the last sync, because
	// last_edited_time has minute precision and the search API lags slightly.
	syncOverlap = 10 * time.Minute
	// The search API misses most pages inside large DBs, so an incremental sync
	// also queries this many of the largest DBs directly.
	bigDBs = 5
)

// Item is one page or DB. Short JSON keys keep the index file small.
type Item struct {
	Title    string `json:"t"`
	Edited   string `json:"e"`
	Created  string `json:"c"`
	Parent   string `json:"p"` // page, DB or block ID; "" for the workspace
	Kind     string `json:"o"` // "page" or "db"
	Archived bool   `json:"x"`
}

type Index struct {
	SyncedAt string           `json:"synced_at"`
	Items    map[string]*Item `json:"items"`
	path     string
}

// New returns an empty index that saves to path.
func New(path string) *Index { return &Index{Items: map[string]*Item{}, path: path} }

func Load(path string) (*Index, error) {
	ix := New(path)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ix, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, ix); err != nil {
		return nil, fmt.Errorf("索引が壊れています（%s）。`nn sync --full` で作り直してください: %w", path, err)
	}
	if ix.Items == nil {
		ix.Items = map[string]*Item{}
	}
	return ix, nil
}

func (ix *Index) Save() error {
	b, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(ix.path, b, 0o600)
}

// Age is the time since the last sync; ok is false if never synced.
func (ix *Index) Age() (time.Duration, bool) {
	t, err := time.Parse(time.RFC3339, ix.SyncedAt)
	if err != nil {
		return 0, false
	}
	return time.Since(t), true
}

func (ix *Index) add(o notion.Obj) {
	par := notion.Map(o, "parent")
	pt := notion.Str(par, "type")
	parent := ""
	if pt != "workspace" {
		parent = notion.Str(par, pt)
	}
	kind := "page"
	if notion.Str(o, "object") == "database" {
		kind = "db"
	}
	ix.Items[notion.Str(o, "id")] = &Item{
		Title:    notion.Title(o),
		Edited:   notion.Str(o, "last_edited_time"),
		Created:  notion.Str(o, "created_time"),
		Parent:   parent,
		Kind:     kind,
		Archived: notion.Bool(o, "archived") || notion.Bool(o, "in_trash"),
	}
}

// ChildCounts counts non-archived items per parent ID.
func (ix *Index) ChildCounts() map[string]int {
	counts := map[string]int{}
	for _, it := range ix.Items {
		if it.Parent != "" && !it.Archived {
			counts[it.Parent]++
		}
	}
	return counts
}

// Sync updates the index. A full sync starts over and queries every DB;
// an incremental one reads only what changed since the last sync.
func (ix *Index) Sync(c *notion.Client, full bool, extraDBs []string, progress io.Writer) (int, error) {
	if full {
		ix.Items = map[string]*Item{}
		ix.SyncedAt = ""
	}
	var stopBefore time.Time
	if t, err := time.Parse(time.RFC3339, ix.SyncedAt); err == nil {
		stopBefore = t.Add(-syncOverlap)
	}
	started := time.Now().UTC()
	n := 0
	add := func(o notion.Obj) {
		ix.add(o)
		if n++; progress != nil && n%500 == 0 {
			fmt.Fprintf(progress, "  ...%d件\n", n)
		}
	}

	// 1) Search API: the whole workspace, newest first, but it misses pages in big DBs.
	search := notion.Obj{"sort": notion.Obj{"direction": "descending", "timestamp": "last_edited_time"}}
	err := c.Paginate("search", search, func(o notion.Obj) error {
		if t, err := time.Parse(time.RFC3339, notion.Str(o, "last_edited_time")); err == nil && !stopBefore.IsZero() && t.Before(stopBefore) {
			return notion.Stop
		}
		add(o)
		return nil
	})
	if err != nil {
		return n, err
	}

	// 2) Query DBs directly: all of them on a full sync, otherwise the biggest ones plus extraDBs.
	dbs := map[string]bool{}
	if stopBefore.IsZero() {
		for id, it := range ix.Items {
			if it.Kind == "db" && !it.Archived {
				dbs[id] = true
			}
		}
	} else {
		for _, id := range ix.LargestDBs(bigDBs) {
			dbs[id] = true
		}
		for _, id := range extraDBs {
			dbs[id] = true
		}
	}
	var q notion.Obj
	if !stopBefore.IsZero() {
		q = notion.Obj{"filter": notion.Obj{
			"timestamp":        "last_edited_time",
			"last_edited_time": notion.Obj{"on_or_after": stopBefore.Format(time.RFC3339)},
		}}
	} else {
		q = notion.Obj{}
	}
	for id := range dbs {
		err := c.Paginate("databases/"+id+"/query", q, func(o notion.Obj) error {
			add(o)
			return nil
		})
		if err != nil && !isSkippable(err) {
			return n, err
		}
	}
	ix.SyncedAt = started.Format(time.RFC3339Nano)
	return n, ix.Save()
}

// Linked DBs and DBs not shared with the integration cannot be queried; skip them.
func isSkippable(err error) bool {
	var e *notion.Error
	return errors.As(err, &e)
}

// LargestDBs returns up to n DB IDs ordered by number of child items.
func (ix *Index) LargestDBs(n int) []string {
	counts := ix.ChildCounts()
	var ids []string
	for id, c := range counts {
		if it := ix.Items[id]; it != nil && it.Kind == "db" && c > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if counts[ids[i]] != counts[ids[j]] {
			return counts[ids[i]] > counts[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > n {
		ids = ids[:n]
	}
	return ids
}

// Hit is a search result.
type Hit struct {
	ID string
	*Item
}

type FindOptions struct {
	Words []string
	InDB  string // parent ID
	Kind  string // "page", "db" or ""
	Since string // YYYY-MM-DD
}

// Find matches titles containing every word (case-insensitive), newest first.
func (ix *Index) Find(o FindOptions) []Hit {
	words := make([]string, len(o.Words))
	for i, w := range o.Words {
		words[i] = strings.ToLower(w)
	}
	var hits []Hit
	for id, it := range ix.Items {
		if it.Archived || (o.Kind != "" && it.Kind != o.Kind) || (o.InDB != "" && it.Parent != o.InDB) {
			continue
		}
		if o.Since != "" && it.Edited[:min(10, len(it.Edited))] < o.Since {
			continue
		}
		t := strings.ToLower(it.Title)
		ok := true
		for _, w := range words {
			if !strings.Contains(t, w) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, Hit{id, it})
		}
	}
	sortHits(hits)
	return hits
}

func sortHits(h []Hit) {
	sort.Slice(h, func(i, j int) bool {
		if h[i].Edited != h[j].Edited {
			return h[i].Edited > h[j].Edited
		}
		return h[i].ID < h[j].ID
	})
}
