package sniff

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"
	"time"

	"mycast/internal/httptool"
)

func TestRealSiteL0(t *testing.T) {
	hc, _ := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	e := New(Config{UA: "Mozilla/5.0 (Linux; Android 13) Chrome/120.0", Timeout: 15 * time.Second})
	e.hx = hc
	playURL := "https://m.xaruihu.com/vodplay/94275-1-1.html"

	// 直接诊断：e.hc（L0 client）拿到的 body 是不是 gzip 压缩
	req, _ := http.NewRequest("GET", playURL, nil)
	req.Header.Set("User-Agent", e.cfg.UA)
	resp, err := e.hc.Do(req)
	if err != nil {
		t.Fatalf("e.hc.Do 失败: %v", err)
	}
	t.Logf("Content-Encoding: %q  ContentLength: %d", resp.Header.Get("Content-Encoding"), resp.ContentLength)
	// 读原始 body
	raw, _ := io.ReadAll(resp.Body)
	t.Logf("原始 body %d B（若 Content-Encoding=gzip，这里还是压缩字节）", len(raw))
	// 试解压
	var ungzipped string
	if resp.Header.Get("Content-Encoding") == "gzip" {
		r, gerr := gzip.NewReader(bytes.NewReader(raw))
		if gerr == nil {
			b2, _ := io.ReadAll(r)
			ungzipped = string(b2)
			t.Logf("解压后 %d B", len(b2))
		}
	}
	// 看 body 里有没有 m3u8
	html := ungzipped
	if html == "" {
		html = string(raw)
	}
	t.Logf("body 含 m3u8: %v", containsAny(html, ".m3u8"))
	// 跑 ResolveStream
	stream, mime, err := e.ResolveStream(playURL)
	if err != nil {
		t.Logf("ResolveStream 失败: %v", err)
	} else {
		t.Logf("✅ L0 取到流: %s (%s)", stream, mime)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
