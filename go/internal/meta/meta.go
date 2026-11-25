// Package meta 媒体元数据归一化：把任意盗版站/苹果CMS 的"主演/导演/地区/年份/简介"
// 归一化成统一 Media 结构。
//
// 设计哲学（学百度 App 的干净卡片，但站无关）：
//   - 不做"识别 CMS 类型再各写解析器"（规则爆炸，换 CMS 就废）
//   - 做"从页面里把元数据字段抠出来"——三路：
//     ① HTML 直接提取（meta itemprop / JSON-LD / 苹果CMS 服务端渲染字段）
//     ② fetch 响应解析（站点 JS 调 API 拿元数据，响应体是 JSON）
//     ③ 全局变量（站点把数据塞进 window.vodDetail / __NEXT_DATA__ / __INITIAL_STATE__）
//   - 三路任一命中就归一，全没命中返回空 Media（降级到只有集数）
package meta

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Media 归一化媒体对象（对齐百度 App 的干净卡片）。
type Media struct {
	Title    string   `json:"title,omitempty"`
	Type     string   `json:"type,omitempty"`     // 电影/剧集/动漫/综艺
	Actors   string   `json:"actors,omitempty"`   // 主演（逗号分隔）
	Director string   `json:"director,omitempty"` // 导演
	Region   []string `json:"region,omitempty"`   // 地区
	Year     string   `json:"year,omitempty"`
	Language string   `json:"language,omitempty"`
	Original string   `json:"original,omitempty"` // 原著（改编自）
	Synopsis string   `json:"synopsis,omitempty"` // 简介
	Score    string   `json:"score,omitempty"`    // 豆瓣评分
	Poster   string   `json:"poster,omitempty"`   // 封面图 URL
	Updated  string   `json:"updated,omitempty"`  // 更新状态（完结/xx集）
}

// 空 Media 判断（降级用）。
func (m Media) Empty() bool {
	return m.Title == "" && m.Actors == "" && m.Director == "" && m.Synopsis == "" && len(m.Region) == 0 && m.Year == ""
}

// FromHTML 从 HTML 提取元数据（三路：meta itemprop / JSON-LD / 苹果CMS 服务端字段）。
func FromHTML(htmlText string) Media {
	m := Media{}

	// ① JSON-LD（正规站最可靠）
	if ld := extractJSONLD(htmlText); ld != nil {
		m = fillFromJSONLD(m, ld)
	}

	// ② meta itemprop（schema.org microdata）
	if m.Title == "" {
		m.Title = firstMeta(htmlText, `(?is)<meta[^>]*itemprop=["']name["'][^>]*content=["']([^"']+)["']`)
	}
	if m.Director == "" {
		m.Director = firstMeta(htmlText, `(?is)<meta[^>]*itemprop=["']director["'][^>]*content=["']([^"']+)["']`)
	}
	if m.Synopsis == "" {
		m.Synopsis = firstMeta(htmlText, `(?is)<meta[^>]*itemprop=["']description["'][^>]*content=["']([^"']+)["']`)
	}
	if m.Poster == "" {
		m.Poster = firstMeta(htmlText, `(?is)<meta[^>]*property=[""]og:image[""][^>]*content=["']([^"']+)["']`)
	}

	// ③ 苹果CMS 服务端渲染字段（部分站把 vod 字段直接渲染进 HTML）
	if m.Title == "" || m.Actors == "" {
		fillFromMacCMS(m, htmlText)
	}

	return m
}

// FromFetchResponse 解析 fetch 响应体（JSON）提取元数据。
// 站点 JS 调 API 拿元数据，响应可能是苹果CMS 标准 JSON 或自定义。
// 苹果CMS 两种返回形态都要处理：
//   - 单对象：{vod_name:..., vod_actor:...}
//   - 列表套壳：{code:1, list:[{vod_name:...}]}（ac=detail 带 wd 搜索时常见）
func FromFetchResponse(body string) Media {
	if strings.TrimSpace(body) == "" {
		return Media{}
	}
	// ① 顶层直接是苹果CMS 对象（有 vod_ 字段）
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err == nil {
		if m := fromMacCMSMap(obj); !m.Empty() {
			return m
		}
		// ② 列表套壳：list 数组里每个元素才是苹果CMS 对象
		if list, ok := obj["list"].([]any); ok && len(list) > 0 {
			if first, ok := list[0].(map[string]any); ok {
				if m := fromMacCMSMap(first); !m.Empty() {
					return m
				}
			}
		}
		// ③ data 套壳（有些站用 data 而非 list）
		if data, ok := obj["data"].(map[string]any); ok {
			if m := fromMacCMSMap(data); !m.Empty() {
				return m
			}
		}
		// ④ JSON-LD 嵌套（schema.org 结构）
		if m := fillFromJSONLDMap(Media{}, obj); !m.Empty() {
			return m
		}
	}
	return Media{}
}

// FromGlobal 从 JS 全局变量提取（window.vodDetail / __NEXT_DATA__ / __INITIAL_STATE__）。
// jsexec 把全局对象序列化后传进来。
func FromGlobal(globalJSON string) Media {
	if strings.TrimSpace(globalJSON) == "" {
		return Media{}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(globalJSON), &obj); err != nil {
		return Media{}
	}
	// 尝试几种常见命名
	for _, key := range []string{"vodDetail", "vod_detail", "detail", "data", "vod"} {
		if sub, ok := obj[key].(map[string]any); ok {
			if m := fromMacCMSMap(sub); !m.Empty() {
				return m
			}
		}
	}
	// 直接当苹果CMS 对象
	if m := fromMacCMSMap(obj); !m.Empty() {
		return m
	}
	// JSON-LD 风格
	if m := fillFromJSONLDMap(Media{}, obj); !m.Empty() {
		return m
	}
	return Media{}
}

// ===== 内部实现 =====

var reJSONLD = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)

func extractJSONLD(htmlText string) map[string]any {
	m := reJSONLD.FindStringSubmatch(htmlText)
	if m == nil || strings.TrimSpace(m[1]) == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(m[1]), &obj); err != nil {
		// 可能是 JSON 数组
		var arr []map[string]any
		if err2 := json.Unmarshal([]byte(m[1]), &arr); err2 == nil && len(arr) > 0 {
			return arr[0]
		}
		return nil
	}
	return obj
}

func fillFromJSONLD(m Media, ld map[string]any) Media {
	if v, ok := ld["@type"].(string); ok {
		m.Type = v
	}
	if v, ok := ld["name"].(string); ok {
		m.Title = v
	}
	if v, ok := ld["actor"].([]any); ok {
		var names []string
		for _, a := range v {
			switch t := a.(type) {
			case string:
				names = append(names, t)
			case map[string]any:
				if nm, ok := t["name"].(string); ok {
					names = append(names, nm)
				}
			}
		}
		if m.Actors == "" {
			m.Actors = strings.Join(names, ",")
		}
	}
	if v, ok := ld["director"].([]any); ok {
		var names []string
		for _, d := range v {
			switch t := d.(type) {
			case string:
				names = append(names, t)
			case map[string]any:
				if nm, ok := t["name"].(string); ok {
					names = append(names, nm)
				}
			}
		}
		if m.Director == "" {
			m.Director = strings.Join(names, ",")
		}
	}
	if v, ok := ld["description"].(string); ok {
		m.Synopsis = v
	}
	if v, ok := ld["datePublished"].(string); ok {
		m.Year = v[:min(4, len(v))]
	}
	if v, ok := ld["image"].(string); ok {
		m.Poster = v
	}
	if v, ok := ld["genre"].([]any); ok {
		if len(v) > 0 {
			if g, ok := v[0].(string); ok {
				m.Type = g
			}
		}
	}
	return m
}

func fillFromJSONLDMap(m Media, obj map[string]any) Media {
	// 同 fillFromJSONLD，但接受任意 map（全局变量/JSON-LD 嵌套）
	if v, ok := obj["@type"].(string); ok && m.Type == "" {
		m.Type = v
	}
	if v, ok := obj["name"].(string); ok && m.Title == "" {
		m.Title = v
	}
	if v, ok := obj["description"].(string); ok && m.Synopsis == "" {
		m.Synopsis = v
	}
	if v, ok := obj["director"].(string); ok && m.Director == "" {
		m.Director = v
	}
	if v, ok := obj["actor"].(string); ok && m.Actors == "" {
		m.Actors = v
	}
	return m
}

// 苹果CMS 字段映射（兼容全/简拼 + 不同键名）
var macCMSFieldMap = map[string]string{
	"vod_name":     "title",
	"vod_actor":    "actors",
	"vod_director": "director",
	"vod_area":     "region",
	"vod_year":     "year",
	"vod_lang":     "language",
	"vod_blurb":    "synopsis",
	"vod_content":  "synopsis",
	"vod_score":    "score",
	"vod_pic":      "poster",
	"vod_remarks":  "updated",
	"type_name":    "type",
}

func fromMacCMSMap(m map[string]any) Media {
	out := Media{}
	for k, v := range m {
		field, ok := macCMSFieldMap[k]
		if !ok {
			continue
		}
		s, _ := v.(string)
		if s == "" {
			continue
		}
		switch field {
		case "title":
			out.Title = s
		case "actors":
			out.Actors = trimField(s)
		case "director":
			out.Director = s
		case "region":
			out.Region = splitRegion(s)
		case "year":
			out.Year = s
		case "language":
			out.Language = s
		case "synopsis":
			if out.Synopsis == "" {
				out.Synopsis = s
			}
		case "score":
			out.Score = s
		case "poster":
			out.Poster = s
		case "updated":
			out.Updated = s
		case "type":
			out.Type = s
		}
	}
	return out
}

func fillFromMacCMS(m Media, htmlText string) {
	// 苹果CMS 服务端渲染：字段值在 data-* 属性或特定 class 里
	// 例：<div class="stui-content__detail">主演：<span>谭松韵,董晴</span>
	reTitle := regexp.MustCompile(`(?is)<h1[^>]*>([^<]+)</h1>`)
	if m.Title == "" {
		if mt := reTitle.FindStringSubmatch(htmlText); mt != nil {
			m.Title = strings.TrimSpace(mt[1])
		}
	}
	// 通用：class 含 "detail" 的容器里的字段行
	// 主演/导演 行
	if m.Actors == "" {
		if mv := firstMeta(htmlText, `(?is)主演[:：]\s*</span><span[^>]*>([^<]+)</span>`); mv != "" {
			m.Actors = mv
		} else if mv := firstMeta(htmlText, `(?is)主演[:：]\s*<span[^>]*>([^<]+)</span>`); mv != "" {
			m.Actors = mv
		}
	}
	if m.Director == "" {
		if mv := firstMeta(htmlText, `(?is)导演[:：]\s*<span[^>]*>([^<]+)</span>`); mv != "" {
			m.Director = mv
		} else if mv := firstMeta(htmlText, `(?is)导演[:：]\s*<span[^>]*>([^<]+)</span>`); mv != "" {
			m.Director = mv
		}
	}
	// 地区行
	if len(m.Region) == 0 {
		if mv := firstMeta(htmlText, `(?is)地区[:：]\s*<span[^>]*>([^<]+)</span>`); mv != "" {
			m.Region = splitRegion(mv)
		} else if mv := firstMeta(htmlText, `(?is)地区[:：]\s*(<span[^>]*>)?([^<]+)`); mv != "" {
			m.Region = splitRegion(mv)
		}
	}
	// 年份
	if m.Year == "" {
		if mv := firstMeta(htmlText, `(?is)上映[:：]\s*<span[^>]*>(\d{4})</span>`); mv != "" {
			m.Year = mv
		}
	}
}

func firstMeta(htmlText, pattern string) string {
	re := regexp.MustCompile(pattern)
	if m := re.FindStringSubmatch(htmlText); m != nil && len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func splitRegion(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// 常见分隔符：/ 、 ， ， , 空格
	s = regexp.MustCompile(`\s*/\s*|\s*，\s*|\s*,\s*`).ReplaceAllString(s, "\x01")
	parts := strings.Split(s, "\x01")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// trimField 清理元数据字段（源数据常带脏字符：": 谭松韵"、首尾空格等）。
func trimField(s string) string {
	s = strings.TrimSpace(s)
	// 去掉开头的 ": " / "：" / "主演" 等脏前缀（360zy 等站的 vod_actor 实测有此问题）
	for _, p := range []string{": ", "：", "主演：", "主演: "} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimPrefix(s, p)
		}
	}
	// 去掉尾部逗号/斜杠
	s = strings.TrimRight(s, ",，/ 	")
	return strings.TrimSpace(s)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
