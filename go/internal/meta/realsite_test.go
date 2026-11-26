package meta

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestRealSiteHTML 实测 4 个真实盗版站：访问详情页，
// 验证 meta.FromHTML 能否抠出元数据（schema.org/JSON-LD/苹果CMS服务端字段）。
// 这是"通用性"真验证——不是专用 API，是站方自己埋的标签。
func TestRealSiteHTML(t *testing.T) {
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second, Delay: 600 * time.Millisecond})
	if err != nil {
		t.Skip(err)
	}
	// 4 个可直连真实站 + 它们一个详情页（搜索"我的山与海"或任意剧）
	sites := []struct{ name, detailURL string }{
		// 详情页（非首页）才有影视元数据
		{"vipjuji", "https://www.vipjuji.com/voddetail/67381.html"},
		{"jilunxi", "https://www.jilunxi.cn/"},
		{"6ye4", "https://www.6ye4.cc/"},
	}
	for _, s := range sites {
		body, err := hc.Get(s.detailURL)
		if err != nil {
			t.Logf("  %-10s ❌ %v", s.name, shortErr(err))
			continue
		}
		if len(body) < 2000 {
			t.Logf("  %-10s ❌ 太小 (%d B)，非详情页", s.name, len(body))
			continue
		}
		m := FromHTML(body)
		// 也测 JSON-LD / schema.org 埋没
		hasJSONLD := contains(body, "application/ld+json")
		hasItemprop := contains(body, "itemprop")
		hasOG := contains(body, "og:title")
		if m.Empty() {
			t.Logf("  %-10s ⚠️ 无元数据 | HTML含JSON-LD=%v itemprop=%v og=%v (%d B)", s.name, hasJSONLD, hasItemprop, hasOG, len(body))
			continue
		}
		t.Logf("  %-10s ✅ %s | 主演=%s 导演=%s 地区=%v 年份=%s | JSON-LD=%v itemprop=%v",
			s.name, m.Title, m.Actors, m.Director, m.Region, m.Year, hasJSONLD, hasItemprop)
	}
}
