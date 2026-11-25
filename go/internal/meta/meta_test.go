package meta

import (
	"strings"
	"testing"
)

// TestFromHTML_JSONLD 正规站 schema.org JSON-LD
func TestFromHTML_JSONLD(t *testing.T) {
	html := `<html><head>
<script type="application/ld+json">
{
  "@type": "Movie",
  "name": "我的山与海",
  "actor": [{"name":"谭松韵"},{"name":"董晴"},{"name":"奚望"}],
  "director": [{"name":"柏杉"}],
  "description": "本片(剧)是一部中国大陆制片作品的国产剧，于2026年上映。",
  "datePublished": "2026",
  "image": "https://example.com/cover.jpg",
  "genre": "剧情"
}
</script>
</head></html>`
	m := FromHTML(html)
	if m.Title != "我的山与海" {
		t.Errorf("Title: got %q want 我的山与海", m.Title)
	}
	if m.Actors != "谭松韵,董晴,奚望" {
		t.Errorf("Actors: got %q", m.Actors)
	}
	if m.Director != "柏杉" {
		t.Errorf("Director: got %q", m.Director)
	}
	if m.Year != "2026" {
		t.Errorf("Year: got %q", m.Year)
	}
	if !strings.Contains(m.Synopsis, "2026") {
		t.Errorf("Synopsis: got %q", m.Synopsis)
	}
	if m.Poster == "" {
		t.Error("Poster 未提取")
	}
	t.Logf("JSON-LD 提取: %+v", m)
}

// TestFromHTML_MetaItemprop meta itemprop 微数据
func TestFromHTML_MetaItemprop(t *testing.T) {
	html := `<html><head>
<meta itemprop="name" content="我的山与海"/>
<meta itemprop="director" content="柏杉"/>
<meta itemprop="description" content="简介内容"/>
<meta property="og:image" content="https://example.com/pic.jpg"/>
</head></html>`
	m := FromHTML(html)
	if m.Title != "我的山与海" {
		t.Errorf("Title: got %q", m.Title)
	}
	if m.Director != "柏杉" {
		t.Errorf("Director: got %q", m.Director)
	}
	if m.Synopsis == "" {
		t.Error("Synopsis 未提取")
	}
	if m.Poster == "" {
		t.Error("Poster 未提取")
	}
	t.Logf("meta itemprop 提取: %+v", m)
}

// TestFromFetchResponse_MacCMS JSON 苹果CMS 字段
func TestFromFetchResponse_MacCMS(t *testing.T) {
	jsonBody := `{"vod_id":1,"vod_name":"我的山与海","vod_actor":"谭松韵,董晴","vod_director":"柏杉","vod_area":"中国大陆/新加坡/马来西亚","vod_year":"2026","vod_lang":"汉语普通话","vod_blurb":"改编自梁晓声原著","vod_score":"8.5","vod_pic":"https://example.com/1.jpg","vod_remarks":"更新30集"}`
	m := FromFetchResponse(jsonBody)
	if m.Title != "我的山与海" {
		t.Errorf("Title: got %q", m.Title)
	}
	if m.Actors != "谭松韵,董晴" {
		t.Errorf("Actors: got %q", m.Actors)
	}
	if len(m.Region) != 3 || m.Region[0] != "中国大陆" {
		t.Errorf("Region: got %v", m.Region)
	}
	if m.Score != "8.5" {
		t.Errorf("Score: got %q", m.Score)
	}
	if m.Updated != "更新30集" {
		t.Errorf("Updated: got %q", m.Updated)
	}
	t.Logf("苹果CMS JSON 提取: %+v", m)
}

// TestFromGlobal 全局变量（window.vodDetail）
func TestFromGlobal(t *testing.T) {
	globalJSON := `{"vodDetail":{"vod_name":"我的山与海","vod_actor":"谭松韵","vod_director":"柏杉","vod_year":"2026"}}`
	m := FromGlobal(globalJSON)
	if m.Title != "我的山与海" {
		t.Errorf("Title: got %q", m.Title)
	}
	if m.Actors != "谭松韵" {
		t.Errorf("Actors: got %q", m.Actors)
	}
	t.Logf("全局变量提取: %+v", m)
}

// TestMedia_Empty 空 Media 判断
func TestMedia_Empty(t *testing.T) {
	if !(Media{}).Empty() {
		t.Error("空 Media 应 Empty()=true")
	}
	if (Media{Title: "x"}).Empty() {
		t.Error("有 Title 的 Media 应 Empty()=false")
	}
}
