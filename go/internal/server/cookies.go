package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CookieEntry 一条固化的 cookie。
type CookieEntry struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Domain  string `json:"domain"`
	Path    string `json:"path"`
	Expires int64  `json:"expires,omitempty"` // unix 秒，0=会话
}

// CookieStore 固化账号：按引擎存 cookie 到磁盘（cookies/<engine>.json）。
// 用户登录某站后 cookie 自动收集存下，WebView 搜索/嗅探时注入复用。
type CookieStore struct {
	mu    sync.Mutex
	dir   string // 数据目录（cookies/ 子目录）
}

// NewCookieStore 创建存储（dir 为数据目录根）。
func NewCookieStore(dir string) *CookieStore {
	if dir == "" {
		dir = "."
	}
	return &CookieStore{dir: dir}
}

// Save 保存某引擎的 cookie 列表（覆盖）。
func (s *CookieStore) Save(engine string, cookies []CookieEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(s.dir, "cookies"), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cookies, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(engine), b, 0o644)
}

// Load 读某引擎固化 cookie（无则空）。
func (s *CookieStore) Load(engine string) []CookieEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(engine))
	if err != nil {
		return nil
	}
	var out []CookieEntry
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// List 列出所有已固化引擎的状态。
func (s *CookieStore) List() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	ents, _ := os.ReadDir(filepath.Join(s.dir, "cookies"))
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".json" {
			engine := e.Name()[:len(e.Name())-5]
			if n := len(s.loadLocked(engine)); n > 0 {
				out[engine] = n
			}
		}
	}
	return out
}

// Clear 清除某引擎 cookie。
func (s *CookieStore) Clear(engine string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(engine))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// HeaderString 把固化 cookie 拼成 Cookie 请求头（"a=1; b=2"）。
// 供 WebView/后端注入使用。
func (s *CookieStore) HeaderString(engine string) string {
	var parts []string
	for _, c := range s.Load(engine) {
		if c.Expires > 0 && c.Expires < time.Now().Unix() {
			continue // 已过期跳过
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}

func (s *CookieStore) path(engine string) string {
	return filepath.Join(s.dir, "cookies", engine+".json")
}

func (s *CookieStore) loadLocked(engine string) []CookieEntry {
	b, err := os.ReadFile(s.path(engine))
	if err != nil {
		return nil
	}
	var out []CookieEntry
	_ = json.Unmarshal(b, &out)
	return out
}
