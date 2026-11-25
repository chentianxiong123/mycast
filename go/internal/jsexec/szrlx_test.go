package jsexec

import (
	"io"
	"testing"
	"time"
)

// TestSZRLXLightHead sz-rlx 用轻头 client 拿 HTML + go-webengine 解析
func TestSZRLXLightHead(t *testing.T) {
	pageURL := "https://www.sz-rlx.com/detail/402011.html"
	// 轻头 client 拿 HTML（纯 HTTP 只 UA，不 httptool 全套头）
	resp, err := lightClientWithUA("Mozilla/5.0 (Linux; Android 13)", 15*time.Second).Get(pageURL)
	if err != nil {
		t.Fatalf("轻头拿 sz-rlx 失败: %v", err)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	resp.Body.Close()
	t.Logf("轻头拿 sz-rlx: HTTP %d | %d B", resp.StatusCode, len(b))

	eng, err := New(Config{UA: "Mozilla/5.0 (Linux; Android 13; SM-S9180) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36", Timeout: 12 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	// go-webengine 解析（轻头 client 拉外部 script）
	title, _ := eng.WEEval(string(b), pageURL, `document.title`)
	nodes, _ := eng.WEEval(string(b), pageURL, `document.querySelectorAll('*').length`)
	t.Logf("标题: %v | 节点: %v", title, nodes)
}
