# 2026-11-10 电脑 WebView 改手机 UA（wails3 Linux 没有公开字段）

## 结论

wails3 v3.1.0 **Linux（WebKitGTK）没有公开改 User-Agent 的字段/方法**。

- `UserAgent` 字段只在：
  - **Mac**：`MacWebviewPreferences.ApplicationNameForUserAgent`（webview_window_options.go:792）
  - **iOS**：`IOS.UserAgent`（application_options.go:409）
  - **Android**：`AndroidOptions.UserAgent`（application_options.go:478）
- **Linux**（`LinuxWindow` 结构，webview_window_options.go:913）**没有 UA 字段**
- **没有** `SetUserAgent()` 方法（WebviewWindow）
- Linux UA 固定 `"wails.io"`：
  `webkit_settings_set_user_agent_with_application_details(settings, "wails.io", "")`（linux_cgo_gtk3.go:1594）

## 能改（要改 wails3 源码）

改 linux_cgo_gtk3.go:1594：
```go
C.webkit_settings_set_user_agent_with_application_details(settings, c.String("手机UA"), c.String(""))
```
把 `"wails.io"` 换成手机 UA（`iPhone` / `Android` 特征）。

**代价**：改依赖库（wails3 在 go module cache，`go mod` 升级会丢，除非 vendor 或 fork）。

## Android 自写壳

`AndroidOptions.UserAgent`（application_options.go:478）**直接能改**（不用动 wails3）。

## 参考

- `/home/a1/go/pkg/mod/github.com/wailsapp/wails/v3@v3.1.0/pkg/application/webview_window_options.go`（640 WebviewPreferences / 772 MacWebviewPreferences / 913 LinuxWindow）
- `.../pkg/application/linux_cgo_gtk3.go:1594`
- `.../pkg/application/application_options.go`（409 IOS.UserAgent / 478 AndroidOptions.UserAgent）
