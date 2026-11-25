package sniff

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestRealSiteEpisodes 真实站集数提取（xaruihu 详情页，看 Extract 能不能提 30 集）
func TestRealSiteEpisodes(t *testing.T) {
	hc, _ := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	detailURL := "https://m.xaruihu.com/voddetail/94275.html"
	html, err := hc.Get(detailURL)
	if err != nil {
		t.Skip(err)
	}
	t.Logf("详情页 %d B", len(html))
	e := New(Default())
	e.hx = hc
	eps := e.Extract(html, detailURL)
	t.Logf("Extract 提到 %d 集", len(eps))
	for i, ep := range eps {
		if i >= 5 {
			t.Logf("  ... (共 %d 集)", len(eps))
			break
		}
		t.Logf("  [%d] %s → %s", ep.Index, ep.Title, ep.URL)
	}
}
