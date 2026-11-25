package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

func TestWEResolveStreamReal(t *testing.T) {
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	eng, err := New(Config{HTTP: hc, UA: "Mozilla/5.0 (Linux; Android 13)", Timeout: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	html, err := hc.Get("https://m.xaruihu.com/vodplay/94275-1-1.html")
	if err != nil {
		t.Skip(err)
	}
	stream, globals, err := eng.WEResolveStream(html, "https://m.xaruihu.com/vodplay/94275-1-1.html")
	if err != nil {
		t.Fatalf("WEResolveStream 失败: %v", err)
	}
	t.Logf("✅ WE 取流: %s  globals=%v", stream, globals)
	if stream == "" {
		t.Log("⚠️ 没取到流（看 globals 里哪个候选命中了）")
	}
}
