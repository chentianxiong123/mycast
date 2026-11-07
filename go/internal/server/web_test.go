package server

// 模板结构校验测试（照 mymusic go/web/templates_test.go 策略）：
//   Go html/template 只校验 {{}} 语法，HTML 标签开闭平衡零检查——
//   残缺模板（div 开 5 层闭 1 层 / script 标签里塞函数体）能编译通过、零报错。
//   浏览器自动修复掩盖问题，页面「看起来能用」但布局错乱 / 全黑，肉眼很难定位。
//   本测试兜住这个生态弱点：编译期发现骨架破坏。
//
//   黑屏教训（2026-11-07）：index.html 被改坏成 33 行（<script src=...> 标签里塞
//   JS 函数体，页面主体/标签栏/搜索框全没了），WebView 下载了 HTML + 加载了
//   hls.min.js 但无 DOM 可渲染 → 桌面壳全黑。本测试编译期拦截这类破坏。

import (
	"html/template"
	"path"
	"regexp"
	"strings"
	"testing"
)

func TestTemplatesParseSyntax(t *testing.T) {
	entries, err := tplFS.ReadDir("web/templates")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || path.Ext(name) != ".html" {
			continue
		}
		if _, err := template.New(name).ParseFS(tplFS, "web/templates/"+name); err != nil {
			t.Errorf("%s 模板语法错误: %v", name, err)
		}
	}
}

// blockTags 需要开闭成对的结构标签（骨架核心）
var blockTags = []string{"div", "span", "button", "details", "summary", "select", "textarea", "nav", "section", "main", "header", "footer", "form", "ul", "ol", "li"}

func TestTemplatesHTMLBalance(t *testing.T) {
	openRe := map[string]*regexp.Regexp{}
	closeRe := map[string]*regexp.Regexp{}
	for _, tag := range blockTags {
		openRe[tag] = regexp.MustCompile(`<` + tag + `[ >]`)
		closeRe[tag] = regexp.MustCompile(`</` + tag + `>`)
	}

	entries, err := tplFS.ReadDir("web/templates")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || path.Ext(name) != ".html" {
			continue
		}
		b, err := tplFS.ReadFile("web/templates/" + name)
		if err != nil {
			t.Fatalf("%s 读失败: %v", name, err)
		}
		content := string(b)

		for _, tag := range blockTags {
			open := len(openRe[tag].FindAllString(content, -1))
			close := len(closeRe[tag].FindAllString(content, -1))
			if open != close {
				t.Errorf("%s <%s> 开闭不平衡：开 %d / 闭 %d（骨架破坏，浏览器自动修复掩盖，布局错乱）",
					name, tag, open, close)
			}
		}
	}
}

// TestTemplatesSkeleton 关键结构必须存在（SPA 骨架完整性）。
// 黑屏教训：index.html 曾被改坏成 33 行（<script src> 里塞 JS 函数，页面主体全没了）。
func TestTemplatesSkeleton(t *testing.T) {
	b, err := tplFS.ReadFile("web/templates/index.html")
	if err != nil {
		t.Fatalf("index.html 读失败: %v", err)
	}
	content := string(b)

	// 必备骨架标记（这些缺失 = 页面白屏/黑屏）
	for _, marker := range []string{"<html", "</html>", "<head", "</head>", "<body", "</body>"} {
		if !strings.Contains(content, marker) {
			t.Errorf("index.html 缺少骨架标记 %q（页面主体被破坏，黑屏根源）", marker)
		}
	}

	// script 标签开闭平衡（曾把 loadEngines 等函数塞进 <script src> 里 → 全黑）
	openScript := len(regexp.MustCompile(`<script[ >]`).FindAllString(content, -1))
	closeScript := len(regexp.MustCompile(`</script>`).FindAllString(content, -1))
	if openScript != closeScript {
		t.Errorf("index.html <script> 开闭不平衡：开 %d / 闭 %d（曾把 JS 函数塞进 <script src> 里导致骨架破坏）", openScript, closeScript)
	}
}
