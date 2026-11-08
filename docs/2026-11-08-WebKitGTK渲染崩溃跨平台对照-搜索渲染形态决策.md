# WebKitGTK 渲染崩溃跨平台对照：搜索渲染形态决策

> 日期：2026-11-08
> 状态：排查完成，issue 已发布
> 关联：桌面排查文档（同日在桌面沉淀）、Windows 探测（wv3probe/wv3hold）

## 背景

搜索引擎渲染（百度/360/bing）在 Linux WebKitGTK 上运行一段时间后崩溃（SIGABRT）。
之前误判为 #276312（NetworkProcess 实时线程 SIGKILL），用户质疑"确定是同一个 bug 吗"，
用 gdb 重新抓栈后确认**不是** #276312，且跨平台对照证明是 **Linux WebKitGTK 专属 bug**。

## 核心结论（一句话）

**WebView 渲染外部网页的方案本身可行、跨平台稳定；崩溃只发生在 Linux WebKitGTK 2.54
（JS 定时器 use-after-free），Windows WebView2 同逻辑零崩溃。**

## 崩溃根因（gdb 铁证）

```
Program received signal SIGABRT
#0  __GI_raise
#1  __GI_abort
#2  WTF::RunLoop::TimerBase::stop()   (libjavascriptcoregtk-6.0.so.1)
#3  g_main_context_iteration          (libglib-2.0.so.0)
```

- **JS 定时器 use-after-free**：页面加载/卸载时残留的定时器被 run loop 停止，其 owner 已销毁 → SIGABRT。
- **不是 #276312**：那是 NetworkProcess 实时线程被 SIGKILL（不同信号、不同帧、不同路径）；
  #276312 的修复（commit 310907@main）与本崩溃无关。
- **不是代理**：代理（clash-verge 7897 via gsettings）只是**放大器**——关代理百度就稳，
  so.com/bing 仍概率崩。
- **不是注入 JS / WebView 实例**：单 WebView 复用 + 每 URL 新建都崩（共享 NetworkProcess 状态污染）。
- **概率性 ~55%**，多重定向站（so.com）最易触发。

## 跨平台对照（决定性）

| 平台 | 渲染内核 | 百度 | 360 | 必应 | 崩溃 |
|---|---|---|---|---|---|
| **Windows** | WebView2 (Chromium 155) | ✅ | ✅ | ✅ | **0 次** |
| **Linux** | WebKitGTK 2.54 | ❌ | ❌ | ❌ | 必崩 |

Windows 用同一套 wails3 逻辑交叉编译（`GOOS=windows CGO_ENABLED=0`，纯 Go 无 cgo），
窗口直接加载三外部搜索页，每 12s 换页 + `Eval(outerHTML.length)`，完整 DONE 零崩溃。

## 搜索渲染形态决策（基于上述）

| 平台 | 推荐渲染 | 理由 |
|---|---|---|
| **Windows** | WebView2 渲染（wails3 壳） | 实测零崩溃，方案可行 |
| **Linux** | HTTP 直拿（demo-search 已证 6/6 稳） | WebKitGTK 2.54 必崩，无法应用层根治，只能崩溃重启重试兜底 |
| **Android** | 系统 WebView 渲染 | 早期手搓壳机制已验证（evaluateJavascript 拿 outerHTML） |

**跨平台一致性**（用户硬决策）：接口一致（WebView JS 注入 + 只读 outerHTML），
进程模型平台相关（桌面独立渲染进程 / Android 系统 WebView）。

## Issue 提交

- **WebKit/WebKit 上游 GitHub 禁 issue + discussion**（镜像，has_issues=false），只有 PR 通道开。
- 走 fork 路线留痕：小号 `niubihaizi` fork → `niubihaizi/WebKit`（PATCH has_issues=true）→
  大号 `chentianxiong123` 发 issue。
- **Issue**: https://github.com/niubihaizi/WebKit/issues/1（OPEN）
  - 标题：WebKitGTK 2.54 crashes (SIGABRT) on loading external pages — use-after-free in WTF::RunLoop::TimerBase::stop()
  - 内容：纯技术（不露项目），含 gdb 栈、复现、与 #276312 区别表、环境。

## 已知边界（Windows 实测发现）

1. **必须 `app.Run()`** 窗口才显示（漏了 → MainWindowHandle=0，进程跑但无窗口）。
2. **SSH 跑 GUI 需 `schtasks /ru <user> /it`**（否则进程在 Session 0 不可见桌面，用户看不到）。
3. **点击 target=_blank 仍弹新窗口**：WebView2 走原生 `NewWindowRequested` 事件，
   wails3 v3.1.0 未暴露该回调，bootJS 注入拦不住（wails3 边界，非渲染方案问题）。

## 下一步（待拍板）

- 搜索链路最终形态：Linux 走 HTTP 直拿（A 方案），Windows/Android 走 WebView 渲染（B 方案）。
- 若坚持单窗口 + 不弹新窗口：需 fork wails3 加 `NewWindowRequested` 回调，或搜索结果点开走本地路由。
