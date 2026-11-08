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
	if _, err := os.Stat(filepath.Join(dir, "library.db")); err != nil {
		t.Fatalf("library.db 未落盘: %v", err)
	}
}

// TestLibraryProgress 进度回写持久化（跨实例读回，SQLite 落盘）。
func TestLibraryProgress(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	l := NewLibrary(dir)
	l.Fav("漫长的季节", "https://d.com/d4.html", "", "")
	l.UpdateProgress("https://d.com/d4.html", 42.5, "00:42:11", "01:40:00")

	// 新实例读回进度
	l2 := NewLibrary(dir)
	it, ok := l2.Get("https://d.com/d4.html")
	if !ok {
		t.Fatal("读回失败")
	}
	if it.Progress != 42.5 {
		t.Fatalf("进度应 42.5, got %v", it.Progress)
	}
	if it.Position != "00:42:11" || it.Duration != "01:40:00" {
		t.Fatalf("位置/时长应持久化, got %q/%q", it.Position, it.Duration)
	}
	// 越界防护
	l2.UpdateProgress("https://d.com/d4.html", 150, "", "")
	it2, _ := l2.Get("https://d.com/d4.html")
	if it2.Progress != 100 {
		t.Fatalf("进度应钳到 100, got %v", it2.Progress)
	}
}

// TestLibraryProgressUpdate 更新进度后 List 里能看到（回写媒体库 UI）。
func TestLibraryProgressUpdate(t *testing.T) {
	l := NewLibrary(t.TempDir())
	l.Fav("狂飙", "https://b.com/d2.html", "", "")
	l.UpdateProgress("https://b.com/d2.html", 66.0, "00:30:00", "00:45:00")
	items := l.List()
	if len(items) != 1 {
		t.Fatalf("应 1 条")
	}
	if items[0].Progress != 66.0 {
		t.Fatalf("List 进度应 66, got %v", items[0].Progress)
	}
}
