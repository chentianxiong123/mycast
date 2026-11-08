package server

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// LibItem 收藏的剧（持久化元信息：纯流媒体源索引，不存文件/内容）。
type LibItem struct {
	Title    string  `json:"title"`
	URL      string  `json:"url"`
	Poster   string  `json:"poster,omitempty"` // 封面图（盗版站详情页抓的，或百度百科）
	Source   string  `json:"source,omitempty"` // 源（pirate/bili/baike）
	Time     string  `json:"time"`
	Progress float64 `json:"progress,omitempty"` // 播放进度百分比 0-100（后端持久化，跨设备/重装不丢）
	Position string  `json:"position,omitempty"` // 最近播放位置（电视侧 HH:MM:SS）
	Duration string  `json:"duration,omitempty"` // 总时长（电视侧 HH:MM:SS）
}

// Library 收藏库：SQLite 持久化到 dataDir/library.db（跟 CookieStore 同级）。
// 媒体库永不产生文件/本地存——只存元信息索引（流媒体源）。
// 用 SQLite（modernc.org/sqlite 纯 Go，CGO_ENABLED=0 交叉编译 Android 可用），
// 播放进度/位置/时长也持久化（跨设备/重装不丢，比 localStorage 稳）。
type Library struct {
	mu  sync.Mutex
	db  *sql.DB
	dir string
}

// NewLibrary 创建收藏库（dir 为数据目录根，建 library.db）。
func NewLibrary(dir string) *Library {
	if dir == "" {
		dir = "."
	}
	ensureDir(dir)
	l := &Library{dir: dir}
	l.open()
	return l
}

func (l *Library) open() {
	path := filepath.Join(l.dir, "library.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return // 打不开则退化：内存库不可用（List 返回空），不 panic
	}
	// 单连接：避免并发写锁冲突（SQLite 文件锁）
	db.SetMaxOpenConns(1)
	// WAL 模式：读写并发不互斥，手机/桌面都稳
	_, _ = db.Exec(`PRAGMA journal_mode=WAL`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS library (
		title    TEXT NOT NULL,
		url      TEXT PRIMARY KEY,
		poster   TEXT DEFAULT '',
		source   TEXT DEFAULT '',
		time     TEXT NOT NULL,
		progress REAL DEFAULT 0,
		position TEXT DEFAULT '',
		duration TEXT DEFAULT ''
	)`)
	l.db = db
}

// close 关库（测试用）。
func (l *Library) close() {
	if l.db != nil {
		_ = l.db.Close()
		l.db = nil
	}
}

// Fav 收藏（同 URL 去重置顶；已有则更新时间并移到最前，更新海报/源）。
func (l *Library) Fav(title, u, poster, source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	// 先删后插：保证重复收藏时 rowid 变最新（ORDER BY time,rowid 置顶）。
	_, _ = l.db.Exec(`DELETE FROM library WHERE url=?`, u)
	_, _ = l.db.Exec(
		`INSERT INTO library(title,url,poster,source,time) VALUES(?,?,?,?,?)`,
		title, u, poster, source, now)
}

// UpdateProgress 更新播放进度（百分比 + 位置 + 时长），回写媒体库。
func (l *Library) UpdateProgress(u string, pct float64, position, duration string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	_, _ = l.db.Exec(
		`UPDATE library SET progress=?, position=?, duration=?,
		   time=strftime('%Y-%m-%d %H:%M:%S','now','localtime') WHERE url=?`,
		pct, position, duration, u)
}

// Unfav 取消收藏。
func (l *Library) Unfav(u string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return
	}
	_, _ = l.db.Exec(`DELETE FROM library WHERE url=?`, u)
}

// List 收藏列表（新→旧，按更新时间倒序）。
func (l *Library) List() []LibItem {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return nil
	}
	rows, err := l.db.Query(
		`SELECT title,url,poster,source,time,progress,position,duration
		 FROM library ORDER BY time DESC, rowid DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []LibItem
	for rows.Next() {
		var it LibItem
		if err := rows.Scan(&it.Title, &it.URL, &it.Poster, &it.Source,
			&it.Time, &it.Progress, &it.Position, &it.Duration); err == nil {
			out = append(out, it)
		}
	}
	return out
}

// Get 查某 URL 是否已收藏。
func (l *Library) Get(u string) (LibItem, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return LibItem{}, false
	}
	row := l.db.QueryRow(
		`SELECT title,url,poster,source,time,progress,position,duration
		 FROM library WHERE url=?`, u)
	var it LibItem
	if err := row.Scan(&it.Title, &it.URL, &it.Poster, &it.Source,
		&it.Time, &it.Progress, &it.Position, &it.Duration); err != nil {
		return LibItem{}, false
	}
	return it, true
}

// ensureDir 确保数据目录存在（sqlite 打开前调）。
func ensureDir(dir string) {
	if dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
}
