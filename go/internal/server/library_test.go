package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryFavList(t *testing.T) {
	l := NewLibrary(t.TempDir())
	l.Fav("繁花", "https://a.com/d1.html", "https://cdn/p1.jpg", "pirate")
	l.Fav("狂飙", "https://b.com/d2.html", "", "pirate")

	items := l.List()
	if len(items) != 2 {
		t.Fatalf("List 应 2 条, got %d", len(items))
	}
	if items[0].Title != "狂飙" { // 新→旧
		t.Fatalf("最新应在前, got %q", items[0].Title)
	}
	// 重复收藏去重置顶
	l.Fav("繁花", "https://a.com/d1.html", "https://cdn/p1.jpg", "pirate")
	items = l.List()
	if len(items) != 2 || items[0].Title != "繁花" {
		t.Fatalf("重复应去重置顶, got %d/%q", len(items), items[0].Title)
	}
	// 取消
	l.Unfav("https://a.com/d1.html")
	if len(l.List()) != 1 {
		t.Fatalf("取消后应 1 条")
	}
}

func TestLibraryPersist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	l := NewLibrary(dir)
	l.Fav("庆余年", "https://c.com/d3.html", "", "")
	// 新实例读盘
	l2 := NewLibrary(dir)
	if _, ok := l2.Get("https://c.com/d3.html"); !ok {
		t.Fatal("持久化应能读回")
	}
	if _, err := os.Stat(filepath.Join(dir, "library.json")); err != nil {
		t.Fatalf("library.json 未落盘: %v", err)
	}
}
