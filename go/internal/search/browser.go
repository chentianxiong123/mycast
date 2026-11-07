// browser.go 小型浏览器自动化框架：搜索引擎用浏览器渲染（不用 fetch）。
//
// 背景：中文搜索引擎（360/百度/Bing）对纯 HTTP fetch 反爬（Accept-Encoding
// 显式设→Go 不解压拿压缩字节、UA/验证码/临时 cookie 各种坑）。用户决策：
// 搜索引擎全走浏览器渲染（Chrome headless 桌面 / Android WebView），
// 像小型浏览器自动化框架：引擎 URL 映射 → 浏览器渲染 → HTML 解析 → 过滤。
package search

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// 浏览器渲染错误。
var (
	errBrowserUnavailable = errors.New("浏览器不可用（Android 无 Chrome，前端走 WebView）")
	errBrowserNoResults   = errors.New("浏览器渲染未提取到结果")
)

// BrowserEngine 一个搜索引擎的浏览器渲染定义。
type BrowserEngine struct {
	Name string // so / baidu / bing
	URL  string // 搜索 URL
}

// BrowserEngines 搜索引擎列表（浏览器渲染用，顺序即优先级）。
func BrowserEngines(query string) []BrowserEngine {
	q := url.QueryEscape(query)
	return []BrowserEngine{
		{"so", "https://www.so.com/s?q=" + q},
		{"baidu", "https://www.baidu.com/s?wd=" + q},
		{"bing", "https://www.bing.com/search?q=" + q},
	}
}

// BrowserSearch 浏览器渲染搜索（框架入口）：
// 依次渲染各引擎（Chrome headless，桌面），ParseRendered 解析，isLegitSite 过滤，
// 首个出真结果的引擎够用（返回其结果 + 引擎名）。
// Android 无 Chrome → 返回 errBrowserUnavailable，前端走 WebView（/api/search/render）。
func (a *Aggregator) BrowserSearch(ctx context.Context, query string) ([]Result, string, error) {
	if FindChrome() == "" {
		return nil, "", errBrowserUnavailable
	}
	seen := map[string]bool{}
	var all []Result
	for _, eng := range BrowserEngines(query) {
		html, err := RenderWithChrome(ctx, eng.URL, 18*time.Second)
		if err != nil || strings.TrimSpace(html) == "" {
			continue
		}
		for _, r := range a.ParseRendered(html, eng.URL, eng.Name) {
			if r.URL == "" || seen[r.URL] || IsLegitSite(r.URL) {
				continue
			}
			seen[r.URL] = true
			all = append(all, r)
		}
		if len(all) > 0 {
			return all, eng.Name, nil // 首个出真结果的引擎够用
		}
	}
	if len(all) == 0 {
		return nil, "", errBrowserNoResults
	}
	return all, "", nil
}
