package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 一条路① 登录：把 BDUSS（/tmp/baidu-cookie-bdu.txt）固化到 cookies/baidu.json（CookieStore 格式）
// 让 search（CookieDir=cookies/）自动读 baidu cookie（含 BDUSS）搜 baidu 过验证。
func main() {
	bdu, _ := os.ReadFile("/tmp/baidu-cookie-bdu.txt")
	cookieStr := strings.TrimSpace(string(bdu))
	type cookieEntry struct {
		Name     string    `json:"name"`
		Value    string    `json:"value"`
		Path     string    `json:"path"`
		Domain   string    `json:"domain"`
		Secure   bool      `json:"secure"`
		HttpOnly bool      `json:"httpOnly"`
		Expires  time.Time `json:"expires"`
	}
	var entries []cookieEntry
	for _, pair := range strings.Split(cookieStr, ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := strings.Index(pair, "=")
		if idx <= 0 {
			continue
		}
		name := pair[:idx]
		entries = append(entries, cookieEntry{
			Name:     name,
			Value:    pair[idx+1:],
			Path:     "/",
			Domain:   ".baidu.com",
			Secure:   true,
			HttpOnly: name == "BDUSS",
		})
	}
	outDir := os.Getenv("MYCAST_COOKIE_DIR")
	if outDir == "" {
		home, _ := os.UserHomeDir()
		outDir = filepath.Join(home, ".local", "share", "mycast", "cookies")
	}
	os.MkdirAll(outDir, 0o755)
	b, _ := json.MarshalIndent(entries, "", "  ")
	f := filepath.Join(outDir, "baidu.json")
	os.WriteFile(f, b, 0o644)
	fmt.Printf("① 登录：固化 %d 条 baidu cookie（含 BDUSS）→ %s\n", len(entries), f)
	// 验证：搜 baidu（带 BDUSS）过验证
	req, _ := http.NewRequest("GET", "https://www.baidu.com/s?wd=我的山与海", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13; SM-S9180) AppleWebKit/537.36 Chrome/120.0 Mobile Safari/537.36")
	req.Header.Set("Cookie", cookieStr)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		fmt.Printf("  搜 baidu: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := readAll(resp)
	fmt.Printf("  搜 baidu（带 BDUSS）: HTTP %d | %d B（过验证=200 结果页）\n", resp.StatusCode, len(body))
}

func readAll(resp *http.Response) ([]byte, error) {
	var out []byte
	buf := make([]byte, 8192)
	for {
		n, err := resp.Body.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return out, nil
}
