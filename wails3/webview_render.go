package main

/*
#cgo pkg-config: webkitgtk-6.0 gtk4
#include <webkit/webkit.h>
#include <gtk/gtk.h>
#include <stdlib.h>

// ── 跨线程渲染（GTK/WebKit 必须在主线程，HTTP handler 是任意 goroutine）──
// 渲染任务投递到 GTK 主线程（g_idle_add），主线程建 WebView + 回调链
// （load-finished → evaluate_javascript → on_finish 拿 outerHTML），
// 结果放全局 + GCond 广播；调用方阻塞等结果。回调链不阻塞主循环。

static GMutex g_mu;
static GCond  g_cond;
static gchar *g_result = NULL;
static gboolean g_done = FALSE;
static gboolean g_inited = FALSE;

// 结果回调（主线程）：JS evaluate 完成后拿 outerHTML。
static void wv_on_finish(GObject *obj, GAsyncResult *res, gpointer user_data) {
	GError *err = NULL;
	JSCValue *v = webkit_web_view_evaluate_javascript_finish(WEBKIT_WEB_VIEW(obj), res, &err);
	if (v) {
		const char *s = jsc_value_to_string(v);
		if (s) {
			g_mutex_lock(&g_mu);
			if (g_result) g_free(g_result);
			g_result = g_strdup(s);
			g_mutex_unlock(&g_mu);
		}
		g_object_unref(v);
	}
	if (err) g_error_free(err);
	g_mutex_lock(&g_mu);
	g_done = TRUE;
	g_cond_broadcast(&g_cond);
	g_mutex_unlock(&g_mu);
}

// load-finished（主线程）：页面加载完 → JS 注入拿 outerHTML。
static void wv_on_load_finished(WebKitWebView *wv, WebKitLoadEvent ev, gpointer user_data) {
	if (ev != WEBKIT_LOAD_FINISHED) return;
	webkit_web_view_evaluate_javascript(wv,
		"document.documentElement.outerHTML", -1, NULL, NULL, NULL, wv_on_finish, NULL);
}

// 主线程任务（g_idle_add）：创建无 UI WebView + 绑回调 + load_uri，立即返回（不阻塞主循环）。
static gboolean wv_render_task(gpointer data) {
	const char *url = (const char*)data;
	if (!g_inited) { gtk_init(); g_inited = TRUE; }
	GtkWidget *wv = webkit_web_view_new(); // 无 UI（不加入任何窗口）
	g_signal_connect(wv, "load-changed", G_CALLBACK(wv_on_load_finished), NULL);
	webkit_web_view_load_uri(WEBKIT_WEB_VIEW(wv), url);
	return G_SOURCE_REMOVE;
}

// renderWebView 无 UI WebView 渲染引擎页：投递主线程 → 回调链 → 阻塞返回 outerHTML。
// 跨平台 WebView（桌面 WebKitGTK），无子窗口 UI。返回 malloc 字符串（调用方 free）。
const char* renderWebView(const char* url, int timeout_ms) {
	g_mutex_lock(&g_mu);
	g_done = FALSE;
	if (g_result) { g_free(g_result); g_result = NULL; }
	g_mutex_unlock(&g_mu);

	g_idle_add(wv_render_task, (gpointer)url);

	// 等结果（主线程回调链跑完 set done）
	g_mutex_lock(&g_mu);
	int waited = 0;
	while (!g_done && waited < timeout_ms) {
		// 用 cond 等待（超时轮询，主线程回调广播）
		g_cond_wait(&g_cond, &g_mu);
		waited += 100;
	}
	gchar *out = g_done ? (g_result ? g_strdup(g_result) : NULL) : NULL;
	g_mutex_unlock(&g_mu);
	return out;
}
*/
import "C"

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// errWebviewRender 无 UI WebView 渲染失败（空 HTML / 超时）。
var errWebviewRender = errors.New("WebView 渲染失败（无 UI WebView，JS 注入拿 outerHTML）")

// webviewRenderHTML 无 UI WebView 渲染引擎搜索页，JS 注入拿 outerHTML，阻塞返回。
// 库 Search 同步调用（WebviewSearchRender 注入点）。跨平台：桌面 WebKitGTK。
//
// 线程安全：库 SearchWithEngine 并行调渲染器（每引擎一 goroutine），但 WebKit/GTK
// 必须主线程 + 全局 C 状态单实例 → 用 mutex 串行化（一次渲染一个引擎）。
func webviewRenderHTML(ctx context.Context, rawURL string, timeout time.Duration) (string, error) {
	webviewMu.Lock()
	defer webviewMu.Unlock()

	if timeout <= 0 {
		timeout = 18 * time.Second
	}
	cu := C.CString(rawURL)
	defer C.free(unsafe.Pointer(cu))
	ms := C.int(timeout / time.Millisecond)
	r := C.renderWebView(cu, ms)
	if r == nil {
		return "", errWebviewRender
	}
	defer C.free(unsafe.Pointer(r))
	out := C.GoString(r)
	if strings.TrimSpace(out) == "" {
		return "", errWebviewRender
	}
	return out, nil
}

// webviewMu 串行化无 UI WebView 渲染（WebKit 主线程 + 全局 C 状态单实例）。
var webviewMu sync.Mutex
