package httptool

import (
	"testing"
	"time"
)

func TestHTTPToolSZRLX(t *testing.T) {
	c, err := New(Config{Timeout: 15 * time.Second}) // 不设 UA，用 DefaultUA（跟 domboss 拿法一致）
	if err != nil {
		t.Fatal(err)
	}
	body, err := c.Get("https://www.sz-rlx.com/detail/402011.html")
	if err != nil {
		t.Logf("Get 报错: %v", err)
		return
	}
	t.Logf("Get 拿到 %d B（之前 10250B=403壳，看现在多少）", len(body))
	// 看有没有数据（<title>《我的山与海》）
	hasData := contains(body, "我的山与海")
	t.Logf("含'我的山与海': %v", hasData)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
