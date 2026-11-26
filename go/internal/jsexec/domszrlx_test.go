package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

func TestDomSZRLX(t *testing.T) {
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	eng, err := New(Config{HTTP: hc, UA: "Mozilla/5.0 (Linux; Android 13) Chrome/120.0", Timeout: 12 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	pageURL := "https://www.sz-rlx.com/detail/402011.html"
	html, ferr := hc.Get(pageURL)
	if ferr != nil {
		t.Logf("⚠️ 抓不到 sz-rlx: %v", ferr)
		return
	}
	t.Logf("HTML %d B", len(html))
	// DOM 解析（go-webengine）
	title, _ := eng.WEEval(html, pageURL, `document.title`)
	nodes, _ := eng.WEEval(html, pageURL, `document.querySelectorAll('*').length`)
	t.Logf("标题: %v | DOM 节点: %v", title, nodes)
	// 找视频标题/集数链接/流（Eval 多种可能）
	vidInfo, _ := eng.WEEval(html, pageURL, `(function(){
		var r = {};
		var h1 = document.querySelector('h1, .title, [class*="title"], [class*="name"]');
		if (h1) r.title = h1.textContent.trim().slice(0,50);
		// 找集数链接
		var eps = [...document.querySelectorAll('a')].filter(a => /play|ep|episode|集|/\\d+/.test(a.href + a.textContent));
		r.eps = eps.length;
		// 找流地址（m3u8/mp4 在 HTML 里）
		var streamEl = document.querySelector('video[src], source[src]');
		if (streamEl) r.videoSrc = streamEl.src || streamEl.getAttribute('src');
		// 找全局变量
		r.hasPlayer = typeof player_aaaa !== "undefined" ? "player_aaaa" : (typeof playerconfig !== "undefined" ? "playerconfig" : "none");
		return JSON.stringify(r);
	})()`)
	t.Logf("DOM 提取: %v", vidInfo)
	// L2 取流（go-webengine）
	weStream, _, weErr := eng.WEResolveStream(html, pageURL)
	if weErr != nil {
		t.Logf("L2 取流: %v", weErr)
	} else if weStream != "" {
		t.Logf("✅ L2 取流: %s", weStream)
	} else {
		t.Logf("L2 取流: 空（流不在 player_aaaa/playerconfig 全局，可能在别处或需 L0 正则）")
	}
}
