package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// LibItem 收藏的剧（持久化元信息：纯流媒体源索引，不存文件/内容）。
type LibItem struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	Poster string `json:"poster,omitempty"` // 封面图（盗版站详情页抓的，或百度百科）
	Source string `json:"source,omitempty"` // 源（pirate/bili/baike）
	Time   string `json:"time"`
}

// Library 收藏库：持久化到 dataDir/library.json（跟 CookieStore 同级）。
// 媒体库永不产生文件/本地存——只存元信息索引（流媒体源）。
type Library struct {
	mu    sync.Mutex
	dir   string
	items []LibItem
	path  string
}

// NewLibrary 创建收藏库（dir 为数据目录根）。
func NewLibrary(dir string) *Library {
	if dir == "" {
		dir = "."
	}
	l := &Library{dir: dir, path: filepath.Join(dir, "library.json")}
	l.load()
	return l
}

func (l *Library) load() {
	b, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &l.items)
}

func (l *Library) save() {
	_ = os.MkdirAll(filepath.Dir(l.path), 0o755)
	b, _ := json.MarshalIndent(l.items, "", "  ")
	_ = os.WriteFile(l.path, b, 0o644)
}

// Fav 收藏（同 URL 去重置顶；已有则更新时间并移到最前）。
func (l *Library) Fav(title, u, poster, source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().Format("2006-01-02 15:04:05")
	for i := range l.items {
		if l.items[i].URL == u {
			// 移到最前 + 更新时间/海报/源
			it := l.items[i]
			it.Time = now
			if poster != "" {
				it.Poster = poster
			}
			if source != "" {
				it.Source = source
			}
			l.items = append(append([]LibItem{it}, l.items[:i]...), l.items[i+1:]...)
			l.save()
			return
		}
	}
	l.items = append([]LibItem{{Title: title, URL: u, Poster: poster, Source: source, Time: now}}, l.items...)
	l.save()
}

// Unfav 取消收藏。
func (l *Library) Unfav(u string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.items[:0]
	for _, it := range l.items {
		if it.URL != u {
			out = append(out, it)
		}
	}
	l.items = out
	l.save()
}

// List 收藏列表（新→旧）。
func (l *Library) List() []LibItem {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]LibItem(nil), l.items...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	return out
}

// Get 查某 URL 是否已收藏。
func (l *Library) Get(u string) (LibItem, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, it := range l.items {
		if it.URL == u {
			return it, true
		}
	}
	return LibItem{}, false
}
