# MyCast 安卓白屏三根因 + gitignore 路径误伤（真机排查记录）

## 背景/起因

APK 装到真机（小米 192.168.31.124，adb 无线）后，App 启动**纯白屏**（WebView 空白），logcat 看到 `SELinux execute_no_trans denied`。桌面版之前是「打开浏览器」，用户明确要 **mymusic 那种 wails3 原生窗口**，不弹浏览器。

## 问题现象

- App 进程在跑（`com.mycast.app`），但 WebView 白屏
- logcat：`avc: denied { execute_no_trans } for path="/data/data/com.mycast.app/files/hc/mycast"`
- 服务端进程 `libmycast.so` 存在但 8080 不监听（IPv4）
- 弹「服务端启动超时」toast

## 排查过程

按时间顺序，**每一步试了什么 → 结果**，失败的也记（重点：为什么不行）。

### 1. SELinux 拒绝执行（第一次根因）

二进制原本打包进 `assets/<abi>/mycast`，启动时拷到 `files/hc/mycast` 再 exec。

- 试：从 `files/` 执行 → **`execute_no_trans denied`**（Android 11+ 禁止从 app 私有目录 exec）
- 解：二进制打包进 **`lib/<abi>/libmycast.so`（nativeLibraryDir）**——PackageManager 解压到 nativeLibraryDir，SELinux 允许 execute（tailscale/syncthing 同法）
- 改：`build-apk.sh` 产物 `assets/<abi>/mycast` → `lib/<abi>/libmycast.so`；`MainActivity.startBackend()` 从 `getApplicationInfo().nativeLibraryDir` 找，不再拷 `files/`
- 结果：SELinux denied 消失，8080 起来（但还白屏）

### 2. Go 服务端绑 IPv6（第二次根因，最隐蔽）

8080 在手机内**只有 IPv6 监听**（`/proc/net/tcp6` 有 `[::]:1F90`，`/proc/net/tcp` IPv4 无），`backendReady` 连 `127.0.0.1:8080`（IPv4）连不上。

- 试 1：改 `:8080` → `0.0.0.0:8080` → **本机复现仍绑 IPv6**（`net.Listen("tcp","0.0.0.0:8080")` → `[::]:8080`）
- 关键：**先本机编译 Linux probe 验证**（用户骂我太急、该先上电脑测）。写 `/tmp/listenprobe.go`，实测：
  - `net.Listen("tcp", "0.0.0.0:18080")` → **`[::]:18080`**（Go 的 `tcp` 网络 + 通配地址实际绑 IPv6 双栈！）
  - `net.Listen("tcp", "127.0.0.1:18081")` → `127.0.0.1:18081`（IPv4）
  - `net.Listen("tcp4", "0.0.0.0:18091")` → **IPv4**（`/proc/net/tcp` 有 `0000:46AB` 0A=LISTEN，无 IPv6）
- 结论：**必须 `tcp4`**。`":port"` 和 `"0.0.0.0:port"` 都绑 IPv6（手机 IPv4 映射未开，桌面双栈能连所以桌面没暴露）
- 改：`main.go` `net.Listen("tcp4", "0.0.0.0:8080")` + `srv.Serve(ln)`
- 结果：手机内 IPv4 8080 监听成功，但还白屏

### 3. backendReady 连根路径 `/` 返回 404（第三次根因）

`backendReady` 连 `BASE = "http://127.0.0.1:8080"`（**根路径 `/`**），要求 `code==200`。

- 关键：mycast 无 `/` 路由（只有 `/ui`、`/api/...`），请求 `/` → **404** → `backendReady` 永远 false → 不 `loadUrl` → 白屏
- 排查：给 `backendReady` 加 `Log.i("MyCast", "backendReady -> "+code)`，重装抓 logcat → **`backendReady -> 404`**（此前 curl 测的是 `/ui` 200，没测根路径，被误导）
- 改：`backendReady` 连 **`UI_URL`（/ui）**（200 路径）
- 结果：`backendReady -> 200`，WebView 加载 `http://127.0.0.1:8080/ui`，**UI 完整渲染**（首页/媒体库/遥控器/设备/设置）

### 4. 桌面版对齐 mymusic（用户要求看架构）

用户明确「去看看另一个项目的架构」。看 `/mnt/shared/my-project/mymusic`：

- mymusic = **wails3 v3.1.0 原生窗口**：`application.New()` + `WebviewWindowOptions.URL = "http://127.0.0.1:PORT"` **直载真实后端 HTTP（非 asset 桥）**——推翻我早前「弃 wails3 三座大山」的结论
- 照做：新建 `mycast/wails3/`（main.go + go.mod `mycast/app` + build/config.yml），`go build` 成功 23M（`dist/mycast-desktop`）
- 验证：启动后 xdotool 找到窗口、后端日志有窗口 `GET /static/hls.min.js`（窗口加载了后端）、**不弹浏览器**
- 注意：SSH 会话无真实 X server（DISPLAY=:0 但无 X socket）→ 窗口 1x1 隐藏；**用户本机桌面有真实显示则正常**。用户确认桌面版能跑

## 解决方案/结论

| 层 | 根因 | 修复 |
|----|------|------|
| SELinux | `files/` exec 二进制被 `execute_no_trans` denied | 二进制打包进 `lib/<abi>/libmycast.so`（nativeLibraryDir） |
| 网络 | Go `":port"`/`"0.0.0.0:port"` 都绑 IPv6 `[::]`，手机 IPv4 映射未开 | `net.Listen("tcp4", "0.0.0.0:8080")`（先本机 probe 验证） |
| 就绪检测 | `backendReady` 连根路径 `/` 返回 404（无 `/` 路由） | 改连 `/ui`（200） |
| 桌面壳 | 打开浏览器（用户不接受） | 新 `wails3/` 原生窗口（URL 直载真实 HTTP） |

最终验证：`backendReady -> 200`，WebView URL=`http://127.0.0.1:8080/ui` title=`MyCast`，body 完整（想看什么/搜索/最近在看/首页·媒体库·遥控器·设备·设置）。App 进程 + libmycast.so + 8080 IPv4 稳定。

## 坑与教训（重点：路径问题）

这次最大的坑其实是**路径问题**，用户特意提醒「记住路径问题」：

1. **`.gitignore` 的 `mycast`（无斜杠）误伤目录**——git pattern 不带斜杠匹配**任何层级**名为 `mycast` 的目录。导致 **`go/cmd/mycast/` 和 `com/mycast/` 从未进仓库**（main.go、MainActivity.java 首次提交才进来！）。修复：`mycast` → `/mycast`（只根目录二进制）。
   - 教训：`.gitignore` 里项目名**必须带 `/` 锚定**（`/mycast`），否则同名目录全被吞。
2. **二进制路径**：Android 11+ 禁止从 `files/` exec（SELinux）→ **必须 nativeLibraryDir**（`lib/<abi>/`），不是任何 app 私有目录都能 exec。
3. **Go 监听地址**：`":port"` 和 `"0.0.0.0:port"` 在 Go 里都绑 IPv6 `[::]:port`（`tcp` 网络 + 通配 = IPv6 双栈）。**只有 `tcp4` 才绑 IPv4**。桌面双栈能连所以桌面没暴露，手机 IPv4 映射未开才炸。
4. **就绪检测路径**：`backendReady` 连根路径 `/`（无路由 → 404），必须连真实 200 路径（`/ui`）。curl 测 `/ui` 200 会误导以为根路径也 200。
5. **产物路径全 gitignore**：`dist/`、`android/dist/`、`android/lib/`、`android/assets/`、`wails3/bin/`——产物不进仓库（之前 dist/ 产物误进仓库，`git rm --cached` 清出）。
6. **SSH 无 X server**：wails3 窗口在 SSH 会话显示 1x1（无真实 X），不是代码问题；用户本机桌面有 DISPLAY 才正常。

## 验证（真机 + 本机）

- 真机 192.168.31.124：`backendReady -> 200`、WebView `/ui` title=`MyCast`、UI 完整渲染、8080 IPv4 监听、进程稳定
- 本机：`listenprobe.go` 验证 `tcp4` 才绑 IPv4；`mycast-desktop`（wails3）启动窗口创建 + 加载后端资源
- 提交：`734c41f`（fix(android) 白屏三根因 + 桌面 wails3 原生窗口 + gitignore 路径误伤），已推送
