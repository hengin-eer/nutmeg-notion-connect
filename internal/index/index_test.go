package index

import (
	"strings"
	"testing"
	"time"
)

func sample() *Index {
	return &Index{SyncedAt: "2026-11-11T00:00:00Z", Items: map[string]*Item{
		"db1": {Title: "Articles", Edited: "2026-11-10T00:00:00Z", Kind: "db"},
		"p1":  {Title: "46th FinanSu MTG", Edited: "2026-11-09T00:00:00Z", Parent: "db1", Kind: "page"},
		"p2":  {Title: "45th FinanSu MTG", Edited: "2025-11-09T00:00:00Z", Parent: "db1", Kind: "page"},
		"p5":  {Title: "43rd 引き継ぎ", Edited: "2024-01-01T00:00:00Z", Parent: "db1", Kind: "page"},
		"p3":  {Title: "Seeds 定例", Edited: "2026-11-01T00:00:00Z", Parent: "db1", Kind: "page"},
		"p4":  {Title: "古いページ", Edited: "2026-11-01T00:00:00Z", Parent: "db1", Kind: "page", Archived: true},
	}}
}

func TestFind(t *testing.T) {
	ix := sample()
	hits := ix.Find(FindOptions{Words: []string{"finansu", "mtg"}})
	if len(hits) != 2 || hits[0].ID != "p1" {
		t.Errorf("find: %+v", hits)
	}
	if hits := ix.Find(FindOptions{Words: []string{"ページ"}}); len(hits) != 0 {
		t.Errorf("archived item returned: %+v", hits)
	}
	if hits := ix.Find(FindOptions{Since: "2026-01-01", InDB: "db1"}); len(hits) != 2 {
		t.Errorf("since: %+v", hits)
	}
}

func TestOverview(t *testing.T) {
	out := sample().Overview(OverviewOptions{Now: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)})
	for _, want := range []string{
		"ページ 4 / DB 1",
		"    4  2026-11-10  Articles  db1",
		"FinanSu 2, MTG 2",
		"46th 1, 45th 1, 43rd 1",
		"最近30日に更新された場所（3件）",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestLargestDBs(t *testing.T) {
	if got := sample().LargestDBs(5); len(got) != 1 || got[0] != "db1" {
		t.Errorf("largest: %v", got)
	}
}
