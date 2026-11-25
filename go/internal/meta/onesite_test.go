package meta

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestXaruihuDetail 实测用户给的站：m.xaruihu.com/voddetail/94275.html
func TestXaruihuDetail(t *testing.T) {
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	body, err := hc.Get("https://m.xaruihu.com/voddetail/94275.html")
	if err != nil {
		t.Fatalf("获取失败: %v", err)
	}
	t.Logf("HTML %d B", len(body))
	t.Logf("含JSON-LD=%v 含itemprop=%v 含og:%v 含苹果CMS字段=%v",
		contains(body, "ld+json"), contains(body, "itemprop"),
		contains(body, "og:"), contains(body, "vod_actor"))
	m := FromHTML(body)
	if m.Empty() {
		t.Errorf("元数据为空")
	}
	t.Logf("✅ %s | 主演=%s | 导演=%s | 地区=%v | 年份=%s | 评分=%s | 更新=%s",
		m.Title, m.Actors, m.Director, m.Region, m.Year, m.Score, m.Updated)
	if m.Synopsis != "" {
		t.Logf("简介=%.60s", m.Synopsis)
	}
	// 也打印 JSON-LD 原文看看站方埋了什么
	if idx := indexOf(body, "ld+json"); idx >= 0 {
		seg := body[idx : idx+400]
		t.Logf("JSON-LD 片段: %.300s", seg)
	}
}
