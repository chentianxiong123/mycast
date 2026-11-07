// logcap.go 全局日志捕获：捕所有包 log.Printf 输出（搜索/嗅探/拿流/投屏），
// 环形缓冲 500 条，/api/logs 读取。辅助调试（用户要求）。
package server

import (
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const logCapMax = 500

type logcap struct {
	mu    sync.Mutex
	lines []string
}

var logBuf = &logcap{}

// CaptureLog 捕获全局 log 输出（所有包 log.Printf 都进环形缓冲）。
// 启动时调用一次；原 stdout 输出保留（终端/日志文件照常）。
func CaptureLog() {
	r, w, _ := os.Pipe()
	log.SetOutput(io.MultiWriter(os.Stderr, w)) // stderr 照常 + 管道捕获
	go func() {
		buf := make([]byte, 0, 64*1024)
		tmp := make([]byte, 4096)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				// 按行切
				for {
					idx := indexByte(buf, '\n')
					if idx < 0 {
						break
					}
					line := string(buf[:idx])
					buf = buf[idx+1:]
					logBuf.add(line)
				}
				if len(buf) > 64*1024 {
					buf = buf[:0] // 防无换行撑爆
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func (l *logcap) add(line string) {
	line = strings.TrimRight(line, "\r\n ")
	if line == "" {
		return
	}
	l.mu.Lock()
	// 已带时间戳的 log 输出直接存；无时间戳补
	if len(line) < 20 || line[4] != '/' {
		line = time.Now().Format("15:04:05") + " " + line
	}
	l.lines = append(l.lines, line)
	if len(l.lines) > logCapMax {
		l.lines = l.lines[len(l.lines)-logCapMax:]
	}
	l.mu.Unlock()
}

// Logs 最近 N 条日志（n<=0=全部）。
func (l *logcap) Logs(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.lines) {
		out := make([]string, len(l.lines))
		copy(out, l.lines)
		return out
	}
	out := make([]string, n)
	copy(out, l.lines[len(l.lines)-n:])
	return out
}
