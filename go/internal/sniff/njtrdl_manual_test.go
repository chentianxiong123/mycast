package sniff

import (
	"strings"
	"testing"
)

// 真实盗版站样本（南京影视 njtrdl 播放页，2026-11-09 抓取）：
// zanpiancms 模板：href 带 \" 转义 + \/ 斜杠转义 + 上一集/下一集导航 + 广告注入脚本 + 199集倒序。
// 抽 5 集核心片段（倒序）+ 导航 + 广告注入，验证 CleanAds→Extract→Sort 全链路。
const njtrdlSample = `<div class="player_playlist">
  <ul class="clearfix fade in active" id="con_playlist_1">
   <li class="list4"><a target="_self" href="\/bofang\/366547-1-199.html">第199集</a></li>
  <li class="list4"><a target="_self" href="\/bofang\/366547-1-198.html">第198集</a></li>
  <li class="list4"><a target="_self" href="\/bofang\/366547-1-197.html">第197集</a></li>
  <li class="list4"><a target="_self" href="\/bofang\/366547-1-2.html">第2集</a></li>
  <li class="list4"><a target="_self" href="\/bofang\/366547-1-1.html">第1集</a></li>
  </ul>
</div>
<div class="prevnext hidden-xs">
<a rel="nofollow" class="b-l b-r" href="/bofang/366547-1-129.html" id="pre" target="_self">上一集</a>
<a href="/bofang/366547-1-131.html" class="b-l" id="next" target="_self">下一集</a>
</div>
<script id="fUreeAguHjEjxeBxq5FB8m1794199724337" src="https://hxuxv7vx0.com/1794200180632.SQJF445uipQ6aTMcw9QvgN.2029.png?rid=1794200180632"></script>
<div class="box-video-text-list"><a href="/forum/394925.html" title="斯诺克北爱尔兰公开赛">热门推荐1</a></div>`

// 真实盗版站一整套验证：CleanAds 删广告 → Extract 提集数（排除上一/下一集）→ Sort 正序 → URL 干净。
func TestNjtrdlRealPage(t *testing.T) {
	raw := njtrdlSample
	adsBefore := strings.Count(raw, "hxuxv7vx0")
	t.Logf("样本 %d 字节, 广告注入 %d 处", len(raw), adsBefore)

	cleaned := CleanAds(raw)
	adsAfter := strings.Count(cleaned, "hxuxv7vx0")
	if adsAfter != 0 {
		t.Errorf("广告脚本未删净: %d 处残留", adsAfter)
	}
	if strings.Contains(cleaned, "不要乱删内容") {
		t.Fatalf("误删: %s", cleaned)
	}

	e := New(Default())
	eps := e.Extract(cleaned, "https://www.njtrdl.com/bofang/366547-1-130.html")
	t.Logf("提取集数: %d 条", len(eps))
	for i, ep := range eps {
		t.Logf("  [%d] %s → %s", i+1, ep.Title, ep.URL)
	}
	if len(eps) != 5 {
		t.Fatalf("期待 5 集（排除上一集/下一集/推荐位），拿到 %d", len(eps))
	}
	SortEpisodes(eps)
	if eps[0].Title != "第1集" || eps[4].Title != "第199集" {
		t.Errorf("排序错: 首=%s 尾=%s", eps[0].Title, eps[4].Title)
	}
	if eps[0].URL != "https://www.njtrdl.com/bofang/366547-1-1.html" {
		t.Errorf("URL 应还原 \\/ 转义: %q", eps[0].URL)
	}
	// 上一集/下一集/推荐位必须排除
	for _, ep := range eps {
		if strings.Contains(ep.Title, "一集") || strings.Contains(ep.Title, "推荐") {
			t.Errorf("误抓导航/推荐: %+v", ep)
		}
	}
}