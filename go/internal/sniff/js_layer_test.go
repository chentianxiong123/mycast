package sniff

import (
	"testing"
	"time"
)

// TestResolveStreamL2JSEngine L2 层集成：纯 HTTP + 正则拿不到流地址时，
// 用嵌入式 JS 引擎执行页面混淆脚本取出真实流地址。
// 这正是以前"只能靠 WebView 渲染"才能搞定的场景。
func TestResolveStreamL2JSEngine(t *testing.T) {
	// 页面特征：流地址被混淆（字符串数组 + 索引函数 + eval 动态拼接），
	// 然后写入 video.src。静态正则/字符串扫描都拿不到完整 URL——
	// 因为源码里不存在完整地址字面量，只有 base64 片段。
	html := `<html><head><title>播放页</title></head><body>
	<div id="player"></div>
	<script>
	var _0xarr = ["aHR0cHM6Ly9jZG4ucmVhbC5leGFtcGxlLmNvbS92b2Qv", "MTIzNC9pbmRleC5tM3U4"];
	function _0xget(n) { return atob(_0xarr[n]); }
	var _0xurl = eval('_0xget(0)+_0xget(1)');
	var _0xv = document.createElement('video');
	_0xv.src = _0xurl;
	document.body.appendChild(_0xv);
	</script>
	</body></html>`

	e := New(Config{EnableJS: true, Timeout: 2 * time.Second})
	got, mime, err := e.resolveStreamJS(html, "https://pirate.example.com/play/1234")
	if err != nil {
		t.Fatalf("resolveStreamJS: %v", err)
	}
	want := "https://cdn.real.example.com/vod/1234/index.m3u8"
	if got != want {
		t.Errorf("应捕获混淆拼装的流地址\n  期望: %s\n  实际: %s", want, got)
	}
	t.Logf("L2 层成功取出流地址: %s (mime=%s)", got, mime)
}
