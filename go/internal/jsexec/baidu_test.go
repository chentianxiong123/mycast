package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestBaiduSearch 操控百度搜影视 → 解析结果页 DOM → 找可进的站（影视站/资源站）
func TestBaiduSearch(t *testing.T) {
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	eng, err := New(Config{HTTP: hc, UA: "Mozilla/5.0 (Linux; Android 13) Chrome/120.0", Timeout: 12 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// 1. 抓百度搜索页（移动端 m.baidu.com 更轻）
	keyword := "我的山与海 在线"
	searchURL := "https://m.baidu.com/s?wd=" + keyword
	html, ferr := hc.Get(searchURL)
	if ferr != nil {
		t.Logf("百度抓不到: %v", ferr)
		// 试 PC 百度
		searchURL = "https://www.baidu.com/s?wd=" + keyword
		html, ferr = hc.Get(searchURL)
		if ferr != nil {
			t.Skipf("百度抓不到: %v", ferr)
		}
	}
	t.Logf("百度搜索页 %d B (%s)", len(html), searchURL)

	// 2. 解析搜索结果（DOM 里的 <a> 链接 + 标题）
	results, _ := eng.WEEval(html, searchURL, `[...document.querySelectorAll('a')].map(a=>({t:a.textContent.trim(), h:a.href})).filter(x=>x.t && x.h && x.h.startsWith('http') && !x.h.includes('baidu')).slice(0,15)`)
	if arr, ok := results.([]interface{}); ok {
		t.Logf("搜索结果 (%d 条):", len(arr))
		for i, r := range arr {
			if i >= 10 {
				break
			}
			t.Logf("  [%d] %v", i+1, r)
		}
	} else {
		t.Logf("搜索结果解析: %v", results)
	}

	// 3. 找"影视站/资源站"链接（进详情页可测的）
	//    启发式：URL 含 vod/youku/bilibili/tencent 或 标题含"在线/播放/免费"
	found, _ := eng.WEEval(html, searchURL, `[...document.querySelectorAll('a')].map(a=>({t:a.textContent.trim(), h:a.href})).filter(x=>/vod|youku|bilibili|tencent|qq.com\/v\/|\.m3u8|免费在线|在线观看|播放/.test(x.h+x.t)).slice(0,10)`)
	if arr, ok := found.([]interface{}); ok && len(arr) > 0 {
		t.Logf("✅ 找到 %d 个可进的影视/资源站链接:", len(arr))
		for _, r := range arr {
			t.Logf("  %v", r)
		}
	} else {
		t.Logf("没找到影视/资源站链接（搜索结果里）")
	}
}
