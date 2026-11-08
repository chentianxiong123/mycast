# 搜索引擎 WebView 渲染方案（跨平台，无 UI 实例 + 只读 JS 注入）

> 日期：2026-11-08
> 状态：桌面已验证可行（WebKitGTK 2.54），Android 待接
> 决策：搜索引擎**全走 WebView 渲染**（跨平台），**不用** Chrome headless / fetch

---

## 1. 为什么是 WebView（决策依据）

用户硬决策：搜索引擎必须全走 **WebView 渲染**，跨平台通用。不是无头 Chrome（手机没有），不是 fetch（引擎反爬识别爬虫）。

| 方案 | 跨平台 | 反爬识别 | 手机 | cookie | 结论 |
|------|--------|---------|------|--------|------|
| Chrome headless | ✗（无手机版） | 中 | 无 | Chrome 自己的存储，难共享 | **否决** |
| fetch（requests） | ✓ | **高**（爬虫特征） | ✓ | 手动 header 拼 | **否决**（易验证码） |
| **WebView 渲染** | **✓**（Android 系统 WebView / WebKitGTK / WebView2） | **低**（真浏览器环境） | ✓ | **WebView 原生存储** | **✅ 采用** |

## 2. 形态：无 UI WebView 实例 + 只读 JS 注入

- **无 UI**：`webkit_web_view_new()` / `new WebView(ctx)` 创建**不加入窗口**的 WebView 实例（用户明确不要子窗口 UI）
- **只读 JS 注入**：`webkit_web_view_evaluate_javascript("document.documentElement.outerHTML")` / `WebView.evaluateJavascript()`——**只序列化 DOM，不执行页面脚本**，风险最小
- 每个系统 WebView 都支持 JS 注入（通用能力）：
  - Android：`WebView.evaluateJavascript()`
  - 桌面 Linux：`webkit_web_view_evaluate_javascript()` / wails3 `Eval()`
  - Windows：`CoreWebView2.ExecuteScriptAsync()`

## 3. 架构（库直接搜，对标 searxng）

```
调用方（HTTP /api/search）
  → go-proxy-search 库 Search(ctx, q, Options{Renderer})
  → 调宿主注入的 RenderFunc（无 UI WebView 渲染引擎页 + 只读 JS 注入拿 outerHTML，阻塞返回）
  → ParseRendered 按引擎解析 → 聚合去重/过滤/限 N
  → 返回 []Result{Engine, Title, URL}   （一个请求出聚合结果）
```

- **库无状态**、无 GUI：不能自己创建 WebView（WebView 是宿主 GUI 组件）
- **宿主注入渲染函数**：`WebviewSearchRender`（桌面 wails3 / Android 壳各自实现）
- 库只管：拿引擎 URL → 调渲染 → 解析 → 聚合。对标 searxng 的 `Search()` 返回聚合结果

## 4. 桌面实现（已验证，WebKitGTK 2.54）

文件：`wails3/webview_render.go`（cgo 直调 WebKitGTK 6.0）

关键点：
1. **无 UI WebView**：`webkit_web_view_new()`（不加入任何窗口）
2. **跨线程投递**：GTK/WebKit 必须在**主线程**。HTTP handler 是任意 goroutine → 用 `g_idle_add` 把渲染任务投递到 GTK 主线程（wails3 的 app.Run 主循环跑它）
3. **回调链**：`load-changed`(FINISHED) → `evaluate_javascript` → `on_finish` 拿 outerHTML——回调链不阻塞主循环
4. **GCond 同步**：调用方阻塞等结果，主线程回调广播
5. **串行化 mutex**：WebKit 主线程 + 全局 C 状态单实例 → 一次渲染一个引擎

> 踩坑：直接在主线程外调 webkit_web_view_new + g_main_context_iteration 会 **SIGSEGV**（WebKit 非线程安全）。必须 g_idle_add 投递主线程 + 回调链。

## 5. 可行性验证（桌面实测）

`/api/search?q=繁花` → 聚合结果：
- **so 7 条**（含盗版站 jilunxi / 0531cc / czarjx + 标题完整）
- **baidu 13 条**
- **bing 1 条**（串行渲染排最后，超时）

各引擎各归各（引擎标签正确），标题完整，盗版站正确识别。真 WebView 渲染（非 Chrome headless），无子窗口 UI。

## 6. 已知代价（诚实记录）

1. **串行渲染慢**：库 Search 并行调渲染器，但 WebKit 单实例 → 串行化 → 3 引擎排队 → 搜索 15-25s，bing 易超时
   - 缓解：先 so+baidu 两引擎；或每引擎独立 WebView 实例（并行）
2. **cgo 复杂**：跨线程投递 + 回调链 + GCond + 串行 mutex——能跑但有维护风险
3. **Android 未做**：要 jni + `WebView.evaluateJavascript` 回传，机制相同

## 7. 未来：cookie / 登录态（WebView 天然优势，最容易）

WebView **原生管理 cookie**，加登录态比 fetch/Chrome 容易且安全：
- **Android**：`CookieManager.getInstance().setCookie(url, cookie)` 一行注入
- **WebKitGTK**：`webkit_web_context_cookie_manager_*`（persistent storage + set_cookie）
- **WebView2**：`CoreWebView2.CookieManager`

**登录流程**：WebView 加载登录页 → 用户登录 → cookie **自动固化在 WebView 自己的存储**（httponly/secure 语义由 WebView 处理）→ 后续搜索**自动带 cookie**（B站/360 登录态）。

**安全**：cookie 走 WebView 存储，**不经过 Go 内存、不经过 HTTP header**，不会被日志/传输泄漏；个人使用 + 只读注入 → 攻击面最小。

## 8. 待办

- [ ] 桌面定型：串行慢优化（so+baidu 或并行实例）
- [ ] Android 接 WebView 渲染（jni + evaluateJavascript）
- [ ] cookie/登录态（WebView 原生存储）
- [ ] 沉淀 exploration-notes（拆仓 go-proxy-search + WebView 方案）
