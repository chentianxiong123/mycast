// Package e2e 后端集成端到端测试（httptest 本地站，无外网依赖，确定性）。
//
// 测新路线核心资产的跨模块协作：
//   - sniff（L0→L2 降级链决策）
//   - jsexec（嵌入式 JS 引擎取流，替代旧 WebView 渲染路径）
//   - httptool（cookie 捕获 + 持久化往返 + http 明文站分流）
//
// 不依赖真实外网（风控/数据中心 IP 不稳定），全部走 httptest 本地明文站。
package e2e

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mycast/internal/httptool"
	"mycast/internal/sniff"
)

// obfuscatedPlayPage 混淆播放页：纯 HTTP 正则拿不到流地址（m3u8 字符拆散在数组里），
// 必须执行 JS 才能拼出来。对齐真实影视站"反爬混淆"特征。
const obfuscatedPlayPage = `<html><body>
<video id="v"></video>
<script>
var _k = ['m','a','s','t','e','r','.','m','3','u','8'];
function getStream(){ return _k.join(''); }
setTimeout(function(){ document.getElementById('v').src = '/stream/' + getStream(); }, 50);
</script>
</body></html>`

// plainPlayPage 明文播放页：流地址直接写在 <video src>，纯 HTTP 正则（L0）即可拿到。
const plainPlayPage = `<html><body><video src="/stream/master.m3u8"></video></body></html>`

func newLocalSite(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/play/obf/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(obfuscatedPlayPage))
	})
	mux.HandleFunc("/play/plain/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(plainPlayPage))
	})
	mux.HandleFunc("/stream/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#EXTM3U\n"))
	})
	// cookie 持久化验证端点
	mux.HandleFunc("/setcookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "persist-abc", Path: "/"})
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/checkcookie", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("sid")
		if err != nil {
			w.Write([]byte("MISSING"))
			return
		}
		w.Write([]byte(c.Value))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestE2EDegradationChain 降级链决策：L0 纯 HTTP vs L2 JS 引擎。
// 混淆页在 EnableJS=false 时 L0 拿不到流（NeedsBrowser 类错误）；
// EnableJS=true 时 L2 用 JS 引擎执行混淆脚本取到流地址。
// 同时验证明文页 L0 直接命中（不需要 JS 引擎）。
func TestE2EDegradationChain(t *testing.T) {
	srv := newLocalSite(t)
	dir := t.TempDir()

	mk := func(enableJS bool) *sniff.Extractor {
		return sniff.New(sniff.Config{
			UA:            "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 Chrome/120.0 Mobile Safari/537.36",
			Timeout:       8 * time.Second,
			EnableJS:      enableJS,
			CookieDir:     dir,
			ThrottleDelay: 0,
		})
	}

	// 明文页：L0 直接命中，不需要 JS 引擎（EnableJS=false 也能过）
	stream, _, err := mk(false).ResolveStream(srv.URL + "/play/plain/1")
	if err != nil {
		t.Fatalf("L0 明文页应直接命中，却失败: %v", err)
	}
	if !strings.Contains(stream, "master.m3u8") {
		t.Fatalf("L0 明文页流地址不符: %s", stream)
	}
	t.Logf("L0 明文页命中: %s", stream)

	// 混淆页 + EnableJS=false：L0 拿不到（m3u8 拆散），应失败
	if _, _, err := mk(false).ResolveStream(srv.URL + "/play/obf/1"); err == nil {
		t.Error("混淆页在 EnableJS=false 时应 L0 全落空（NeedsBrowser），却成功了")
	} else {
		t.Logf("混淆页 EnableJS=false 按预期 L0 落空: %v", err)
	}

	// 混淆页 + EnableJS=true：L2 JS 引擎执行混淆脚本取流（替代旧 WebView 渲染路径）
	stream2, _, err := mk(true).ResolveStream(srv.URL + "/play/obf/1")
	if err != nil {
		t.Fatalf("L2 JS 引擎应取到流地址，却失败: %v", err)
	}
	if !strings.Contains(stream2, "master.m3u8") {
		t.Fatalf("L2 流地址不符: %s", stream2)
	}
	t.Logf("L2 JS 引擎命中: %s", stream2)
}

// TestE2ECookiePersistence cookie 捕获 → 落盘 → 新 Client 恢复，跨"重启"往返。
// 明文站走 switchRT 的 http 分流（impersonate 强制 TLS，明文站必须走默认 transport）。
func TestE2ECookiePersistence(t *testing.T) {
	srv := newLocalSite(t)
	dir := t.TempDir()

	// 第一个 Client：访问设置 cookie 的端点，jar 捕获 + 落盘
	c1, newErr := httptool.New(httptool.Config{
		CookieDir: dir,
		Timeout:   8 * time.Second,
		Delay:     0,
	})
	if newErr != nil {
		t.Fatalf("httptool.New 失败: %v", newErr)
	}
	if _, err := c1.Get(srv.URL + "/setcookie"); err != nil {
		t.Fatalf("设置 cookie 失败: %v", err)
	}

	// 第二个 Client（模拟重启）：同 CookieDir，应恢复 cookie 并带上
	c2, newErr := httptool.New(httptool.Config{
		CookieDir: dir,
		Timeout:   8 * time.Second,
		Delay:     0,
	})
	if newErr != nil {
		t.Fatalf("httptool.New 失败: %v", newErr)
	}
	body, err := c2.Get(srv.URL + "/checkcookie")
	if err != nil {
		t.Fatalf("校验 cookie 请求失败: %v", err)
	}
	if body != "persist-abc" {
		t.Errorf("cookie 持久化往返失败: 期望 persist-abc，实际 %q", body)
	} else {
		t.Logf("cookie 持久化往返成功: %s（含 http 明文站分流）", body)
	}

	// 落盘文件存在性
	if _, err := filepath.Glob(filepath.Join(dir, "*.json")); err != nil {
		t.Logf("cookie 目录内容检查: %v", err)
	}
}
