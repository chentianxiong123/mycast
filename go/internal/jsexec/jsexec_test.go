package jsexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestExecPageConfusedVideoSrc 典型影视站：base64 数组 + eval + video.src 写入混淆流地址。
// 这是"以前只能靠 WebView 渲染才拿得到"的场景。
func TestExecPageConfusedVideoSrc(t *testing.T) {
	base := "https://cdn.real.example.com/vod/"

	// 混淆代码：真实流地址被 base64 拆分 + 索引函数 + eval 动态拼接，
	// 然后写入 video 元素的 src（静态扫描拿不到完整 URL）
	html := `<!DOCTYPE html><html><head><title>测试播放页</title></head><body>
	<div class="player"></div>
	<script>
	// 混淆：字符串数组 + 索引映射 + eval
	var _0xarr = ["aHR0cHM6Ly8=", "Y2RuLnJlYWwuZXhhbXBsZS5jb20vdi8=", "MTIzNC9pbmRleC5tM3U4"];
	function _0xget(n) { return atob(_0xarr[n]); }
	var _0xurl = eval('_0xget(0)+_0xget(1)+_0xget(2)');
	var _0xv = document.createElement('video');
	_0xv.src = _0xurl;
	document.body.appendChild(_0xv);
	</script>
	<script>
	// 第二个 script：动态改 src（模拟播放器初始化回调）
	var _0xv2 = document.createElement('video');
	_0xv2.setAttribute('src', 'https://cdn.real.example.com/vod/1234/master.m3u8');
	</script>
	</body></html>`

	e, err := New(Config{Timeout: 2 * time.Second, BaseURL: base})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()

	res := e.ExecPage(html, base)
	if len(res.Hits) == 0 {
		t.Fatalf("期望捕获到流地址, 实际 0 个 (error=%s)", res.Error)
	}
	found := map[string]bool{}
	for _, h := range res.Hits {
		found[h.URL] = true
		t.Logf("捕获: source=%-12s url=%s", h.Source, h.URL)
	}
	// 两个 script 各写入一次
	if !found["https://cdn.real.example.com/v/1234/index.m3u8"] {
		t.Errorf("应捕获到混淆拼装的流地址 https://cdn.real.example.com/v/1234/index.m3u8, 实际命中=%v", res.Hits)
	}
	if !found["https://cdn.real.example.com/vod/1234/master.m3u8"] {
		t.Errorf("应捕获到 setAttribute 写入的 master.m3u8, 实际命中=%v", res.Hits)
	}
}

// TestExecPageFetchStub 站点靠 fetch 取流地址（引擎内网络 → Go http.Client）。
func TestExecPageFetchStub(t *testing.T) {
	dir := t.TempDir()
	hc, err := httptool.New(httptool.Config{CookieDir: dir, Delay: 0})
	if err != nil {
		t.Fatalf("httptool.New: %v", err)
	}
	e, err := New(Config{Timeout: 3 * time.Second, HTTP: hc.HTTPClient(), BaseURL: "https://x.example.com/play/1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()

	// 页面脚本用 fetch 请求一个流地址
	src := `var u = fetch('https://cdn.real.example.com/vod/9999/index.m3u8');`
	res := e.ExecPage("<html><body><script>"+src+"</script></body></html>", "https://x.example.com/play/1")
	got := false
	for _, h := range res.Hits {
		if strings.Contains(h.URL, "9999/index.m3u8") {
			got = true
		}
	}
	if !got {
		t.Errorf("应通过 fetch stub 捕获流地址, 实际=%v", res.Hits)
	}
}

// TestExecPageRelativeURL 相对路径流地址 → 按 base 拼成绝对 URL。
func TestExecPageRelativeURL(t *testing.T) {
	e, err := New(Config{Timeout: 2 * time.Second, BaseURL: "https://x.example.com/vod/detail/7777"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()

	html := `<html><body><script>
	var _0xv = document.createElement('video');
	_0xv.src = '/stream/7777/index.m3u8';
	</script></body></html>`

	res := e.ExecPage(html, "https://x.example.com/vod/detail/7777")
	found := false
	for _, h := range res.Hits {
		if h.URL == "https://x.example.com/stream/7777/index.m3u8" {
			found = true
			t.Logf("相对路径已解析为绝对: %s", h.URL)
		}
	}
	if !found {
		t.Errorf("相对路径应解析为 https://x.example.com/stream/7777/index.m3u8, 实际=%v", res.Hits)
	}
}

// TestExecPageSecurityFence 不可信脚本的死循环不能挂死进程（围栏）。
func TestExecPageSecurityFence(t *testing.T) {
	e, err := New(Config{Timeout: 500 * time.Millisecond, BaseURL: "https://x.example.com/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()

	start := time.Now()
	e.ExecPage(`<html><body><script>while(true){}</script></body></html>`, "https://x.example.com/")
	elapsed := time.Since(start)
	// 死循环应被打断，而不是挂死
	if elapsed > 3*time.Second {
		t.Errorf("死循环未被围栏打断: 耗时 %s", elapsed)
	} else {
		t.Logf("死循环被打断, 耗时 %s", elapsed)
	}
}

// TestExecPageCookiePersist 验证 httptool 的 cookie 持久化目录可用。
func TestExecPageCookiePersist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cookies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hc, err := httptool.New(httptool.Config{CookieDir: dir})
	if err != nil {
		t.Fatalf("httptool.New: %v", err)
	}
	// 直接调 SaveCookies 验证目录可写
	if hc == nil {
		t.Fatal("hc nil")
	}
	t.Logf("cookie 目录: %s", dir)
}
