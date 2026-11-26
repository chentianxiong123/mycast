package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"mycast/internal/jsexec"

	"github.com/andybalholm/brotli"
)

func main() {
	cookie, _ := os.ReadFile("/tmp/baidu-cookie-bdu.txt")
	c := &http.Client{Timeout: 15 * time.Second}
	u := "https://www.baidu.com/s?wd=" + url.QueryEscape("我的山与海 免费在线播放") + "&rn=20"
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.baidu.com/")
	req.Header.Set("Cookie", strings.TrimSpace(string(cookie)))
	resp, _ := c.Do(req)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var s string
	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		gz, _ := gzip.NewReader(bytes.NewReader(raw))
		b2, _ := io.ReadAll(gz)
		s = string(b2)
	case "br":
		out := bytes.NewBuffer(nil)
		io.Copy(out, brotli.NewReader(bytes.NewReader(raw)))
		s = out.String()
	default:
		s = string(raw)
	}
	fmt.Printf("baidu HTML %d B，jsexec 真 DOM 解析...\n", len(s))
	jse, _ := jsexec.New(jsexec.Config{})
	defer jse.Close()
	// baidu 结果在 DOM 的 <a>（真实 href），jsexec 真 DOM 执行
	jsOut, err := jse.WEEval(s, u, `[...document.querySelectorAll('h3 a, .result a, c-container a')].map(a=>({t:(a.textContent||'').trim().slice(0,30), h:a.href||''})).filter(x=>x.h&&x.h.startsWith('http')&&!x.h.includes('baidu.com')).slice(0,12)`)
	if err != nil {
		fmt.Println("WEEval err:", err)
	} else {
		fmt.Printf("jsexec WEEval（baidu 结果 <a> 真实 href）:\n%s\n", fmt.Sprint(jsOut))
	}
}
