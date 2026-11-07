// logcap.go 日志捕获（独立 log 文件，不劫持全局 log.SetOutput）。
//
// 黑屏教训：曾用 log.SetOutput(io.MultiWriter(os.Stderr, pipe)) 劫持全局 log，
// 导致 wails3 WebView 初始化（走 log 的 AssetServer/Build Info）被 pipe 阻塞/干扰
// → WebView 发不出 GET /ui → 桌面壳黑屏。
//
// 现方案：logcap 开独立 log.Logger（Append|Create，写到 ~/.mycast/mycast.log），
// 各包调用 logger.Printf 记日志；/api/logs 读该文件。不影响全局 log、不干扰 WebView。
package server

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// logFile 独立日志文件路径（~/.mycast/mycast.log）。
var logFile string

// captureLogger 独立 log.Logger（不 SetOutput 全局，避免干扰 wails3 WebView）。
var captureLogger = log.New(os.Stderr, "", log.Ltime)

var logOnce sync.Once

// CaptureLog 初始化独立日志文件 + logger（不影响全局 log，不干扰 wails3 WebView）。
// 各包改用 logger.Printf(...) 记日志；/api/logs 读 logFile。
func CaptureLog() {
	logOnce.Do(func() {
		// 数据目录：桌面 ~/.mycast，安卓由 server.New 注入 dataDir（这里默认 home）
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir := filepath.Join(home, ".mycast")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		logFile = filepath.Join(dir, "mycast.log")
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			captureLogger = log.New(f, "", log.Ltime)
		}
	})
}

// Logger 取独立 logger（各包 log.Printf 改用 logger.Printf）。
func Logger() *log.Logger { return captureLogger }

// 日志等级（前缀标记，/api/logs?level= 过滤）。
const (
	LInfo  = "INFO"
	LWarn  = "WARN"
	LError = "ERROR"
)

// Log 记一条 INFO 日志。
func Log(format string, args ...any) { logLevel(LInfo, format, args...) }

// Logf 同上（别名）。
func Logf(format string, args ...any) { logLevel(LInfo, format, args...) }

// LogInfo 记 INFO 级日志。
func LogInfo(format string, args ...any) { logLevel(LInfo, format, args...) }

// LogWarn 记 WARN 级日志。
func LogWarn(format string, args ...any) { logLevel(LWarn, format, args...) }

// LogError 记 ERROR 级日志。
func LogError(format string, args ...any) { logLevel(LError, format, args...) }

func logLevel(level, format string, args ...any) {
	captureLogger.Printf("["+level+"] "+format, args...)
}

// ReadLog 读日志文件最后 n 行（n<=0=全读，最多 500 行），level 非空则过滤等级。
func ReadLog(n int, level string) []string {
	if logFile == "" {
		return nil
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}
	lines := splitLines(string(data))
	// 等级过滤（[INFO]/[WARN]/[ERROR] 前缀）
	if level != "" {
		var f []string
		for _, l := range lines {
			if strings.Contains(l, "["+level+"]") {
				f = append(f, l)
			}
		}
		lines = f
	}
	max := 500
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	if n > 0 && n < len(lines) {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
