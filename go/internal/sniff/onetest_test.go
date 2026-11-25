package sniff

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestOneShot 一条龙：详情页 → 名字/简介 + 30集 + 第一集拿流
func TestOneShot(t *testing.T) {
	hc, _ := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	detailURL := "https://m.xaruihu.com/voddetail/94275.html"
	html, err := hc.Get(detailURL)
	if err != nil {
		t.Skip(err)
	}
	e := New(Default())
	e.hx = hc
	sd := e.ResolveSiteDetail(html, detailURL)
	t.Logf("Media: Title=%q Actors=%q Region=%v", sd.Media.Title, sd.Media.Actors, sd.Media.Region)
	t.Logf("Episodes: %d 集", len(sd.Episodes))
	if len(sd.Episodes) > 0 {
		t.Logf("  第一集: %s", sd.Episodes[0].URL)
	}
	t.Logf("Stream: %s (%s) Err=%v Source=%s", sd.Stream, sd.Mime, sd.Err, sd.Source)
	if sd.Err != nil {
		t.Logf("⚠️ 拿流失败: %v", sd.Err)
	} else {
		t.Logf("✅ 一条龙拿到: 名字=%q + %d集 + 流=%s", sd.Media.Title, len(sd.Episodes), sd.Stream)
	}
}
