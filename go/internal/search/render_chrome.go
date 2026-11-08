package search

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ChromePath 缓存检测到的 chrome/chromium 路径。
var ChromePath string

// FindChrome 检测系统 chrome/chromium（桌面版渲染器；Android 版用 WebView 不需要）。
// 返回路径或 ""。
func FindChrome() string {
	if ChromePath != "" {
		return ChromePath
	}
	for _, cand := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(cand); err == nil {
			ChromePath = p
			return p
		}
	}
	// 常见固定路径兜底
	for _, p := range []string{
		"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
	} {
		if _, err := os.Stat(p); err == nil {
			ChromePath = p
			return p
		}
	}
	return ""
}

// RenderWithChrome 用系统 Chrome headless 渲染 URL，返回渲染后的 HTML。
// virtual-time-budget 让 JS/网络回调跑完再 dump（搜索引擎空壳靠它出真结果）。
// 零重量级依赖：只 exec 系统 chrome（Blink 内核 = Android WebView 同源）。
func RenderWithChrome(ctx context.Context, rawURL string, timeout time.Duration) (string, error) {
	chrome := FindChrome()
	if chrome == "" {
		return "", errorsNew("未找到系统 Chrome（桌面版渲染需要）")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 独立 user-data-dir：带 cookie 态（模拟浏览器上下文，360 需要）
	dir, _ := os.MkdirTemp("", "mycast-chrome-*")
	defer os.RemoveAll(dir)

	cmd := exec.CommandContext(cctx, chrome,
		"--headless", "--disable-gpu", "--no-sandbox",
		"--user-data-dir="+dir,
		"--virtual-time-budget=8000",
		"--dump-dom", rawURL,
	)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		if cctx.Err() != nil {
			return "", errorsNew("Chrome 渲染超时")
		}
		return "", errorsNew("Chrome 执行失败: " + err.Error())
	}
	return out.String(), nil
}

// errorsNew 避免引入额外错误包依赖冲突的小工具。
func errorsNew(msg string) error {
	return &simpleErr{msg}
}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }
