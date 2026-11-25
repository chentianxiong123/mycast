// webengine.go L2 增强：用 go-webengine（纯 Go，真 DOM + goja 执行 JS + Eval）。
//
// 与 hand-crafted dom.go 的区别：
//   - dom.go：我手写假 DOM（8 个方法），真实站 JS 碰 insertBefore/appendChild 会崩
//   - webengine.go：go-webengine 真 DOM（完整 DOM 树 + goja JS），jQuery/播放器不崩，
//     且能 Eval 拿站方 JS 算出的全局变量（流/元数据/集数）
//
// 零 CGO，交叉编译到 android/arm64 + linux/amd64 通。
package jsexec

import (
	"context"
	"net/http"
	"time"

	"github.com/go-webengine/engine/dom"
	wgjs "github.com/go-webengine/engine/js"
)

// lightClient 纯 HTTP 轻头 client（只设 UA，不设 Sec-Fetch/Referer/Sec-CH-UA）。
// 资源站/官方站/技术站反爬松到中等，轻头就通（WAF BIC 判"全套浏览器头 + 无 cookie = 爬虫"→ 403，
// 轻头"普通客户端"反而放行）。go-webengine 拉外部 <script>/数据用轻头，不撞 BIC。
func (eng *Engine) lightClient() *http.Client {
	c := &http.Client{Timeout: eng.cfg.Timeout}
	return c
}

// setLightUA 给请求设轻头 UA（go-webengine 的 js.Client 是 *http.Client，
// 它的 Transport 不设 UA——这里包一层 RoundTripper 注入轻头 UA）。
func lightClientWithUA(ua string, timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout}
	c.Transport = &lightUATransport{base: http.DefaultTransport, ua: ua}
	return c
}

type lightUATransport struct {
	base http.RoundTripper
	ua   string
}

func (t *lightUATransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 只设 UA（轻头，不 Sec-Fetch/Referer/Sec-CH-UA）——资源站 WAF 放行
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", t.ua)
	}
	return t.base.RoundTrip(req)
}

// WEResolveStream 用 go-webengine 执行页面 JS 取流地址（L2 增强层）。
// 返回取到的流地址（m3u8/mp4）+ Eval 的全局变量 + 错误。
func (eng *Engine) WEResolveStream(htmlText, pageURL string) (stream string, globals map[string]interface{}, err error) {
	root, err := dom.Parse(htmlText)
	if err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), eng.cfg.Timeout)
	defer cancel()

	sess := wgjs.Begin(root, wgjs.Options{
		PageURL:        pageURL,
		UserAgent:      eng.cfg.UA,
		Client:         lightClientWithUA(eng.cfg.UA, eng.cfg.Timeout), // 轻头（纯 HTTP 只 UA），资源站 WAF 放行
		Ctx:            ctx,
		Timeout:        eng.cfg.Timeout,
		ViewportWidth:  390,
		ViewportHeight: 844,
	})
	// 跑页面 JS（jQuery/播放器，真 DOM 不崩）
	sess.RunInitialScripts()
	sess.RunPending()
	sess.DispatchLifecycle()

	// Eval 拿站方 JS 塞的全局变量（流地址常见：player_aaaa.url / playerconfig.url / window.detail）
	candidates := []string{
		`typeof player_aaaa !== "undefined" ? (player_aaaa.url || player_aaaa.url_next) : null`,
		`typeof playerconfig !== "undefined" ? (playerconfig.url || playerconfig.vid) : null`,
		`typeof playerdata !== "undefined" ? (playerdata.url || playerdata.video) : null`,
		`typeof videoData !== "undefined" ? (videoData.url || videoData.vid) : null`,
		`typeof detail !== "undefined" ? detail.url : null`,
		`typeof vod !== "undefined" ? (vod.url || vod.play) : null`,
	}
	stream = ""
	globals = map[string]interface{}{}
	for _, c := range candidates {
		v, e2 := sess.Eval(c)
		if e2 != nil {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			stream = s
			break
		}
	}
	// 元数据：Eval 站方全局
	if d, e2 := sess.Eval(`typeof detail !== "undefined" ? detail : null`); e2 == nil {
		if m, ok := d.(map[string]interface{}); ok {
			globals["detail"] = m
		}
	}
	return stream, globals, nil
}

// WEExtractMeta 用 go-webengine 执行 JS + Eval 取元数据（主演/导演/地区...）。
func (eng *Engine) WEExtractMeta(htmlText, pageURL string) map[string]interface{} {
	root, err := dom.Parse(htmlText)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), eng.cfg.Timeout)
	defer cancel()
	sess := wgjs.Begin(root, wgjs.Options{
		PageURL:        pageURL,
		UserAgent:      eng.cfg.UA,
		Client:         lightClientWithUA(eng.cfg.UA, eng.cfg.Timeout), // 轻头（纯 HTTP 只 UA），资源站 WAF 放行
		Ctx:            ctx,
		Timeout:        eng.cfg.Timeout,
		ViewportWidth:  390,
		ViewportHeight: 844,
	})
	sess.RunInitialScripts()
	sess.RunPending()
	sess.DispatchLifecycle()
	v, _ := sess.Eval(`
		(function() {
			try {
				var m = {};
				// 常见全局元数据
				if (typeof detail !== "undefined" && detail) m.detail = detail;
				if (typeof vod !== "undefined" && vod) m.vod = vod;
				if (typeof pageData !== "undefined" && pageData) m.pageData = pageData;
				// <h1 class="title"> 标题（真 DOM 查）
				var t = document.querySelector('h1.title');
				if (t) m.title = t.textContent.trim();
				return m;
			} catch (e) { return {}; }
		})()
	`)
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

// 编译期断言：确保时间类型对齐
var _ time.Duration = time.Second

// WEEval 用 go-webengine 执行页面 JS + Eval 拿任意结果（真 DOM，站方 JS 不崩）。
// 通用入口：取标题/集数/流/元数据都可。
func (eng *Engine) WEEval(htmlText, pageURL, expr string) (interface{}, error) {
	root, err := dom.Parse(htmlText)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), eng.cfg.Timeout)
	defer cancel()
	sess := wgjs.Begin(root, wgjs.Options{
		PageURL:        pageURL,
		UserAgent:      eng.cfg.UA,
		Client:         lightClientWithUA(eng.cfg.UA, eng.cfg.Timeout), // 轻头（纯 HTTP 只 UA），资源站 WAF 放行
		Ctx:            ctx,
		Timeout:        eng.cfg.Timeout,
		ViewportWidth:  390,
		ViewportHeight: 844,
	})
	sess.RunInitialScripts()
	sess.RunPending()
	sess.DispatchLifecycle()
	return sess.Eval(expr)
}
