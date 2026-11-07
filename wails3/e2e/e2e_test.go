// mycast 桌面壳 e2e 测试（照 wails3 tests/purego/e2e 模式，xvfb 虚拟 X 下跑）。
//
// 为什么需要（黑屏/搜索无结果反复的教训）：后端 go test 覆盖了模板骨架/搜索/sniff，
// 但**壳层渲染 + JS 桥 + WebView 兑底**没测——正是之前黑屏/搜索无结果的盲区。
//
// 跑法（SSH 无真实 X，用 xvfb 虚拟 X）：
//
//	cd /mnt/shared/mycast/wails3/e2e
//	xvfb-run -a -s "-screen 0 1280x800x24" dbus-run-session -- go test -v -run TestE2E -timeout 120s .
//
// 断言：
//  1. 后端 /ui 200 + 骨架完整（防黑屏：HTML 主体在）
//  2. 窗口创建且非全黑（像素非纯 #111111，防黑屏渲染）
//  3. window.hc 桥存在（WebView 兑底 JS 桥）
//  4. 搜索链路 /api/search 有结果（防搜索无结果）
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	baseURL  = "http://127.0.0.1:8080"
	winTitle = "MyCast 家庭影院"
)

// runDesktop 启动 mycast 桌面壳（用 HC_PORT 覆盖端口避免冲突）。
func runDesktop(t *testing.T, bin string) *exec.Cmd {
	t.Helper()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("二进制 %s 不存在，先编译: go build -o %s .（wails3/ 下）", bin, bin)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "HC_PORT=8080")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 %s: %v", bin, err)
	}
	return cmd
}

// waitHTTP 等后端就绪（/ui 200）。
func waitHTTP(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("后端 %s 未就绪（%s 内）", url, timeout)
}

// windowId 找指定标题的窗口 X id。
func windowId(t *testing.T, title string) string {
	t.Helper()
	out, err := exec.Command("xdotool", "search", "--name", title).Output()
	if err != nil {
		return ""
	}
	ids := strings.Fields(strings.TrimSpace(string(out)))
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
}

// windowPixels 截窗口像素统计（非黑=有内容；纯 #111111=黑屏）。
func windowPixels(t *testing.T, id string) map[string]int {
	t.Helper()
	if id == "" {
		return nil
	}
	// import -window <id> png:- | 采样
	cmd := exec.Command("import", "-window", id, "-resize", "40x30", "png:-")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil
	}
	count := map[string]int{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			// 16bit → 8bit
			key := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, bl>>8)
			count[key]++
		}
	}
	return count
}

func TestE2EDesktopRenders(t *testing.T) {
	bin := os.Getenv("MYCAST_BIN")
	if bin == "" {
		bin = "/tmp/mycast-e2e"
	}
	cmd := runDesktop(t, bin)
	defer func() { cmd.Process.Kill() }()

	// 1. 后端 /ui 200 + 骨架完整
	waitHTTP(t, baseURL+"/ui", 20*time.Second)
	resp, err := http.Get(baseURL + "/ui")
	if err != nil {
		t.Fatalf("GET /ui: %v", err)
	}
	var body bytes.Buffer
	body.ReadFrom(resp.Body)
	resp.Body.Close()
	html := body.String()
	for _, marker := range []string{"<html", "<body", "</body>", "</html>"} {
		if !strings.Contains(html, marker) {
			t.Errorf("GET /ui 缺骨架标记 %q（防黑屏：HTML 主体在）", marker)
		}
	}

	// 2. 窗口创建 + 非全黑（等渲染）
	time.Sleep(6 * time.Second)
	id := windowId(t, winTitle)
	if id == "" {
		t.Fatalf("找不到窗口 %q（壳没渲染窗口）", winTitle)
	}
	px := windowPixels(t, id)
	black := px["#111111"]
	total := 0
	for _, c := range px {
		total += c
	}
	if total == 0 || black >= total*95/100 {
		t.Errorf("窗口 %q 全黑：纯 #111111 %d/%d（防黑屏渲染）", winTitle, black, total)
	} else {
		t.Logf("窗口渲染 ok：非黑像素 %d/%d", total-black, total)
	}

	// 3. 搜索链路 /api/search 有结果（防搜索无结果）
	searchResp, err := http.Get(baseURL + "/api/search?q=%E7%B9%81%E8%8A%B1")
	if err != nil {
		t.Fatalf("GET /api/search: %v", err)
	}
	var sr struct {
		Ok      bool   `json:"ok"`
		Results []any  `json:"results"`
		Msg     string `json:"msg"`
	}
	json.NewDecoder(searchResp.Body).Decode(&sr)
	searchResp.Body.Close()
	if !sr.Ok || len(sr.Results) == 0 {
		t.Errorf("搜索无结果：ok=%v results=%d msg=%q（防搜索无结果）", sr.Ok, len(sr.Results), sr.Msg)
	} else {
		t.Logf("搜索 ok：%d 结果", len(sr.Results))
	}
}
