package sniff

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

func TestL2Direct(t *testing.T) {
	hc, _ := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	e := New(Config{UA: "Mozilla/5.0 (Linux; Android 13)", Timeout: 15 * time.Second, EnableJS: true})
	e.hx = hc
	// 测 2 个站：xaruihu（L0 够）+ vipjuji（看 L0 vs L2 差异）
	for _, u := range []string{
		"https://m.xaruihu.com/vodplay/94275-1-1.html",
		"https://www.vipjuji.com/vodplay/67381-1-1.html",
	} {
		html, err := hc.Get(u)
		if err != nil {
			t.Logf("%s 抓不到: %v", u, err)
			continue
		}
		// L0
		l0, _, l0err := e.ResolveStream(u)
		// L2（直接调）
		l2, _, l2err := e.resolveStreamJS(html, u)
		t.Logf("%s\n  L0: %s (err=%v)\n  L2: %s (err=%v)", u, l0, l0err, l2, l2err)
	}
}
