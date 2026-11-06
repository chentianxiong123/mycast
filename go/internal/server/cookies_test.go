package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCookieStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	s := NewCookieStore(dir)

	// 初始空
	if got := s.Load("so"); got != nil {
		t.Fatalf("初始应为空, got %v", got)
	}

	// 保存
	cookies := []CookieEntry{
		{Name: "SN", Value: "abc", Domain: ".so.com", Path: "/"},
		{Name: "Q", Value: "def", Domain: "so.com", Path: "/"},
	}
	if err := s.Save("so", cookies); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 读回
	got := s.Load("so")
	if len(got) != 2 || got[0].Name != "SN" {
		t.Fatalf("load 不对: %+v", got)
	}

	// List
	list := s.List()
	if list["so"] != 2 {
		t.Fatalf("list 不对: %+v", list)
	}

	// HeaderString
	hs := s.HeaderString("so")
	if hs != "SN=abc; Q=def" {
		t.Fatalf("header 不对: %q", hs)
	}

	// Clear
	if err := s.Clear("so"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := s.Load("so"); got != nil {
		t.Fatalf("清除后应空, got %v", got)
	}
}

func TestCookieStoreExpiredSkipped(t *testing.T) {
	dir := t.TempDir()
	s := NewCookieStore(dir)
	// 一个已过期（expires 远过去）
	s.Save("bd", []CookieEntry{
		{Name: "live", Value: "1", Domain: ".baidu.com"},
		{Name: "dead", Value: "2", Domain: ".baidu.com", Expires: 1000},
	})
	hs := s.HeaderString("bd")
	if hs != "live=1" {
		t.Fatalf("过期未跳过: %q", hs)
	}
}

func TestCookieStoreFileLocation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s := NewCookieStore(dir)
	s.Save("so", []CookieEntry{{Name: "a", Value: "b"}})
	if _, err := os.Stat(filepath.Join(dir, "cookies", "so.json")); err != nil {
		t.Fatalf("cookie 文件未落到预期位置: %v", err)
	}
}
