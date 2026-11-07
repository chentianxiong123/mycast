package com.mycast.app;

import android.app.Activity;
import android.net.wifi.WifiManager;
import android.os.Bundle;
import android.webkit.CookieManager;
import android.webkit.JavascriptInterface;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;

/**
 * mycast 安卓壳（自写 WebView，不用 wails3）：
 *   内嵌 Go 服务端（assets/<abi>/mycast，GOOS=android 交叉编译）
 *   启动：拷出二进制 → 拉起 127.0.0.1:8080 → 轮询就绪 → WebView 直载 /ui
 *   单端口同源，无跨域；SSDP 组播锁（发现电视）。
 *
 * WebView 兜底（wails3 做不到的，这里自由）：
 *   前端 window.hc.sniffRender(url) → loadUrl(盗版站)      // 加载外部站
 *   onPageFinished(外部站) → 注入 JS 轮询（等 JS 挑战/集数）
 *   → window.hc.renderDone(outerHTML, url)                  // JS 回传
 *   → Java 网络栈 POST /api/sniff/render（无混合内容限制）  // 回传 Go 解析
 *   → loadUrl(/ui)                                          // 跳回界面
 *
 * 账号固化（登录态 cookie 持久化）：
 *   前端 window.hc.loginEngine(engine, loginURL) → 打开登录页（登录模式）
 *   用户登录成功（URL 离开登录域）→ 收集该域 cookie → POST /api/cookie/save
 *   → loadUrl(/ui)。之后 searchEngine 搜索前注入固化 cookie。
 */
public class MainActivity extends Activity {

    private static final int PORT = 8080;
    private static final String BASE = "http://127.0.0.1:" + PORT;
    private static final String UI_URL = BASE + "/ui";

    private Process backend;
    private WebView web;
    private volatile String loginEngine = null; // 非空=登录模式（正在等登录完成）
    private volatile String searchMode = null;  // 非空=搜索渲染模式（等结果链接出现）

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        web = new WebView(this);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);            // localStorage（收藏/设置持久化）
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setUserAgentString(s.getUserAgentString() + " HomeCastRelay/1.0");
        // 允许 cookie（登录态持久化必需）
        CookieManager cm = CookieManager.getInstance();
        cm.setAcceptCookie(true);
        cm.setAcceptThirdPartyCookies(web, true);
        web.setWebViewClient(new WebViewClient() {
            @Override
            public void onPageFinished(WebView view, String url) {
                super.onPageFinished(view, url);
                if (url.startsWith(BASE)) return; // 本机后端不管
                if (loginEngine != null) {
                    // 登录模式：等用户登录完（URL 离开登录域 → 收集 cookie）
                    if (isLoggedIn(url)) {
                        finishLogin();
                    } else {
                        // 还在登录域：注入 JS 轮询等跳转
                        injectLoginWatcher();
                    }
                    return;
                }
                if (searchMode != null) {
                    // 搜索渲染模式：等结果链接出现（JS 渲染完）→ 回传 outerHTML
                    injectSearchExtract();
                    return;
                }
                // 普通外部站：WebView 兜底（集数提取）
                injectSniffExtract();
            }
        });
        // JS 桥：前端调 window.hc.sniffRender(url) 触发兜底；renderDone 回传 outerHTML
        web.addJavascriptInterface(new HcBridge(), "hc");
        // 调试：让桌面 Chrome 能连上 WebView 看 console（chrome://inspect），排查白屏
        if (android.os.Build.VERSION.SDK_INT >= 19) {
            WebView.setWebContentsDebuggingEnabled(true);
        }
        setContentView(web);

        acquireMulticastLock(); // SSDP 组播锁（发现电视）
        startBackendIfNeeded();
    }

    /** 是否已离开登录域（登录成功会跳走）。登录域域名前缀。 */
    private boolean isLoggedIn(String url) {
        String host = urlHost(url);
        // 离开登录/passport 域即视为已登录（跳回主页/搜索）
        return !(host.contains("passport") || host.contains("login") || host.contains("account"));
    }

    /** 收集当前引擎域 cookie → POST /api/cookie/save → 跳回 /ui。 */
    private void finishLogin() {
        final String engine = loginEngine;
        loginEngine = null;
        new Thread(() -> {
            try {
                String cookie = CookieManager.getInstance().getCookie("https://" + engineHost(engine));
                if (cookie != null && !cookie.isEmpty()) {
                    String json = cookiesToJSON(cookie);
                    saveCookiesToBackend(engine, json);
                } else {
                    runOnUiThread(() -> Toast.makeText(this, "未取到 " + engine + " cookie", Toast.LENGTH_LONG).show());
                }
            } catch (Exception e) {
                runOnUiThread(() -> Toast.makeText(this, "登录收集失败: " + e.getMessage(), Toast.LENGTH_LONG).show());
            }
            runOnUiThread(() -> web.loadUrl(UI_URL));
        }).start();
    }

    /** 登录模式：注入 JS 轮询，等页面跳转（登录成功）后回传。 */
    private void injectLoginWatcher() {
        web.evaluateJavascript(
                "(function(){var tries=0;var iv=setInterval(function(){tries++;" +
                "  if(tries>60||window.location.host.indexOf('passport')<0&&window.location.host.indexOf('login')<0){clearInterval(iv);" +
                "    try{window.hc.loginDone();}catch(e){}}" +
                "},500);})();",
                null);
    }

    /** 引擎 host（取 cookie 用）。 */
    private String engineHost(String engine) {
        if ("so".equals(engine)) return "so.com";
        if ("baidu".equals(engine)) return "baidu.com";
        if ("bing".equals(engine)) return "bing.com";
        return engine;
    }

    /** 把 cookie 头字符串转 JSON 数组（name/value/domain/path）。 */
    private String cookiesToJSON(String cookieHeader) {
        StringBuilder sb = new StringBuilder("[");
        String[] parts = cookieHeader.split("; ");
        boolean first = true;
        for (String p : parts) {
            int eq = p.indexOf('=');
            if (eq <= 0) continue;
            if (!first) sb.append(",");
            first = false;
            sb.append("{\"name\":\"").append(p.substring(0, eq))
              .append("\",\"value\":\"").append(p.substring(eq + 1))
              .append("\",\"domain\":\"\",\"path\":\"/\"}");
        }
        sb.append("]");
        return sb.toString();
    }

    /** POST cookie JSON 到 /api/cookie/save。 */
    private void saveCookiesToBackend(String engine, String json) throws IOException {
        HttpURLConnection c = (HttpURLConnection) new URL(BASE + "/api/cookie/save?engine=" + engine).openConnection();
        c.setRequestMethod("POST");
        c.setDoOutput(true);
        c.setConnectTimeout(8000);
        c.setReadTimeout(8000);
        c.setRequestProperty("Content-Type", "application/json");
        OutputStream out = c.getOutputStream();
        out.write(json.getBytes("UTF-8"));
        out.close();
        c.getResponseCode();
        c.disconnect();
    }

    /** URL host 提取。 */
    private String urlHost(String url) {
        try {
            return new URL(url).getHost();
        } catch (Exception e) {
            return "";
        }
    }


    /** 注入 JS：等 JS 挑战完成/集数容器出现（最多 15s）→ 回传 outerHTML + URL。
     * 用 window.hc.renderDone 回传（JS 接口，不受跨域/混合内容限制）。 */
    private void injectSniffExtract() {
        final String js =
                "(function(){" +
                "var tries=0;" +
                "var iv=setInterval(function(){" +
                "  tries++;" +
                "  var d=document;" +
                "  var html=d.documentElement?d.documentElement.outerHTML:'';" +
                "  var hasPlay=d.querySelector('#con_playlist a,[class*=\"playlist\"] a,[id*=\"playlist\"] a,ul li a');" +
                "  if(hasPlay||tries>30){" +   // 有集数 或 15s 超时
                "    clearInterval(iv);" +
                "    try{window.hc.renderDone(html,d.location.href);}catch(e){}" +
                "  }" +
                "},500);" +
                "})();";
        web.evaluateJavascript(js, null);
    }

    /** 搜索渲染模式：等结果链接出现（搜索引擎 JS 渲染完）→ 回传 outerHTML + URL。 */
    private void injectSearchExtract() {
        final String js =
                "(function(){" +
                "var tries=0;" +
                "var iv=setInterval(function(){" +
                "  tries++;" +
                "  var d=document;" +
                "  var html=d.documentElement?d.documentElement.outerHTML:'';" +
                "  var hasRes=d.querySelector('li.res-list a,h3.res-title a,.b_algo a,[class*=\"result\"] a');" +
                "  if(hasRes||tries>30){" +   // 有结果 或 15s 超时
                "    clearInterval(iv);" +
                "    try{window.hc.searchRenderDone(html,d.location.href);}catch(e){}" +
                "  }" +
                "},500);" +
                "})();";
        web.evaluateJavascript(js, null);
    }

    /** JS 桥（前端 → Java）。 */
    class HcBridge {
        /** 前端触发 WebView 兜底：加载盗版站详情页。 */
        @JavascriptInterface
        public void sniffRender(String url) {
            runOnUiThread(() -> web.loadUrl(url));
        }
        /** JS 回传 outerHTML（挑战完）→ Java POST 回 Go 后端 → 跳回 /ui。 */
        @JavascriptInterface
        public void renderDone(String html, String url) {
            new Thread(() -> {
                try {
                    postToBackend("/api/sniff/render", html, url);
                } catch (IOException e) {
                    runOnUiThread(() -> Toast.makeText(MainActivity.this, "嗅探回传失败: " + e.getMessage(), Toast.LENGTH_LONG).show());
                }
                runOnUiThread(() -> web.loadUrl(UI_URL));
            }).start();
        }
        /** 账号固化：打开登录页，进入登录模式（用户登录后自动收集 cookie）。 */
        @JavascriptInterface
        public void loginEngine(String engine, String loginURL) {
            loginEngine = engine;
            runOnUiThread(() -> web.loadUrl(loginURL));
        }
        /** 登录模式 JS 回传：离开登录域 → 收集固化。 */
        @JavascriptInterface
        public void loginDone() {
            runOnUiThread(() -> finishLogin());
        }
        /** 搜索前注入固化 cookie → 加载搜索引擎结果页（渲染后解析）。 */
        @JavascriptInterface
        public void searchEngine(String engine, String searchURL) {
            searchMode = engine;
            runOnUiThread(() -> {
                // 注入固化 cookie（CookieManager 带域）
                String host = engineHost(engine);
                String cookie = getFrozenCookie(engine);
                if (cookie != null && !cookie.isEmpty()) {
                    CookieManager cm = CookieManager.getInstance();
                    for (String p : cookie.split("; ")) {
                        int eq = p.indexOf('=');
                        if (eq > 0) {
                            cm.setCookie("https://" + host, p.substring(0, eq) + "=" + p.substring(eq + 1) + "; path=/; domain=." + host);
                        }
                    }
                    cm.flush();
                }
                web.loadUrl(searchURL);
            });
        }
        /** 搜索渲染完成回传（outerHTML）→ POST /api/search/render → 跳回 /ui。 */
        @JavascriptInterface
        public void searchRenderDone(String html, String url) {
            final String engine = searchMode;
            searchMode = null;
            new Thread(() -> {
                try {
                    postToBackend("/api/search/render", html, url);
                } catch (IOException e) {
                    runOnUiThread(() -> Toast.makeText(MainActivity.this, "搜索回传失败: " + e.getMessage(), Toast.LENGTH_LONG).show());
                }
                runOnUiThread(() -> web.loadUrl(UI_URL));
            }).start();
        }
        /** 读固化 cookie（从后端 /api/cookie/get）。 */
        private String getFrozenCookie(String engine) {
            try {
                HttpURLConnection c = (HttpURLConnection) new URL(BASE + "/api/cookie/get?engine=" + engine).openConnection();
                c.setConnectTimeout(4000);
                c.setReadTimeout(4000);
                java.io.BufferedReader br = new java.io.BufferedReader(new java.io.InputStreamReader(c.getInputStream(), "UTF-8"));
                StringBuilder sb = new StringBuilder();
                String line;
                while ((line = br.readLine()) != null) sb.append(line);
                br.close();
                c.disconnect();
                // 解析 JSON cookies[].name=value
                String body = sb.toString();
                java.util.regex.Matcher m = java.util.regex.Pattern.compile("\"name\":\"([^\"]+)\",\"value\":\"([^\"]+)\"").matcher(body);
                StringBuilder out = new StringBuilder();
                boolean first = true;
                while (m.find()) {
                    if (!first) out.append("; ");
                    first = false;
                    out.append(m.group(1)).append("=").append(m.group(2));
                }
                return out.toString();
            } catch (Exception e) {
                return "";
            }
        }
    }

    /** Java 网络栈 POST 到 Go 后端（不受 WebView 混合内容限制）。 */
    private void postToBackend(String path, String html, String url) throws IOException {
        HttpURLConnection c = (HttpURLConnection) new URL(BASE + path).openConnection();
        c.setRequestMethod("POST");
        c.setDoOutput(true);
        c.setConnectTimeout(8000);
        c.setReadTimeout(8000);
        String body = "html=" + URLEncoder.encode(html, "UTF-8") +
                "&url=" + URLEncoder.encode(url, "UTF-8");
        c.setRequestProperty("Content-Type", "application/x-www-form-urlencoded");
        OutputStream out = c.getOutputStream();
        out.write(body.getBytes("UTF-8"));
        out.close();
        c.getResponseCode();
        c.disconnect();
    }

    /** 拿 WifiManager.MulticastLock：Android 默认不收组播包（SSDP 用），不加收不到电视 M-SEARCH 应答。 */
    private void acquireMulticastLock() {
        try {
            WifiManager wifi = (WifiManager) getApplicationContext().getSystemService(WIFI_SERVICE);
            if (wifi != null) {
                WifiManager.MulticastLock lock = wifi.createMulticastLock("mycast-ssdp");
                lock.setReferenceCounted(true);
                lock.acquire();
            }
        } catch (Exception e) {
            // 无 wifi 锁权限时忽略（SSDP 可能受限，但应用仍可用）
        }
    }

    /** 后台线程：探测 8080 已就绪（旧实例/后台重建）直接复用；否则拷二进制+启动；轮询就绪后 load。 */
    private void startBackendIfNeeded() {
        new Thread(() -> {
            try {
                if (!backendReady()) {
                    startBackend();
                }
                long deadline = System.currentTimeMillis() + 12000;
                while (System.currentTimeMillis() < deadline) {
                    if (backendReady()) {
                        runOnUiThread(() -> web.loadUrl(UI_URL));
                        return;
                    }
                    try { Thread.sleep(400); } catch (InterruptedException ignored) {}
                }
                runOnUiThread(() -> Toast.makeText(this, "服务端启动超时", Toast.LENGTH_LONG).show());
            } catch (IOException e) {
                String msg = e.getMessage();
                runOnUiThread(() -> Toast.makeText(this, "启动失败: " + msg, Toast.LENGTH_LONG).show());
            }
        }).start();
    }

    /** 启动内嵌服务端：从 nativeLibraryDir 执行 libmycast.so（Android 11+ SELinux 禁止 files/ exec，
     *  nativeLibraryDir 允许 execute——二进制打包进 lib/<abi>/libmycast.so，PackageManager 已解压）。 */
    private void startBackend() throws IOException {
        File bin = findNativeBinary();
        if (bin == null) {
            throw new IOException("nativeLibraryDir 找不到 libmycast.so");
        }
        bin.setExecutable(true, true);

        File dir = new File(getFilesDir(), "hc");
        if (!dir.exists()) dir.mkdirs();
        ProcessBuilder pb = new ProcessBuilder(bin.getAbsolutePath());
        pb.environment().put("HCRELAY_PORT", Integer.toString(PORT));
        pb.environment().put("HOME", getFilesDir().getAbsolutePath());
        pb.redirectErrorStream(true);
        pb.redirectOutput(new File(dir, "mycast.log"));
        backend = pb.start();
    }

    /** 找 nativeLibraryDir 的 libmycast.so（已由 PackageManager 解压到对应 ABI）。 */
    private File findNativeBinary() {
        String nativeDir = getApplicationInfo().nativeLibraryDir;
        if (nativeDir == null) return null;
        File f = new File(nativeDir, "libmycast.so");
        return f.exists() ? f : null;
    }

    /** 找设备可用 ABI 服务端：SUPPORTED_ABIS 优先，回退到已内置目录。 */
    private String pickAbi() {
        String[] fallbacks = {"arm64-v8a", "x86_64"};
        for (String abi : android.os.Build.SUPPORTED_ABIS) {
            try {
                getAssets().open(abi + "/mycast").close();
                return abi;
            } catch (IOException ignored) {}
        }
        for (String abi : fallbacks) {
            try {
                getAssets().open(abi + "/mycast").close();
                return abi;
            } catch (IOException ignored) {}
        }
        throw new RuntimeException("assets 缺少 mycast（arm64-v8a / x86_64）");
    }

    private boolean backendReady() {
        try {
            // 检查 /ui（200 路径）；根路径 / 无路由返回 404（旧连 BASE 导致永远 404 → 白屏）
            HttpURLConnection c = (HttpURLConnection) new URL(UI_URL).openConnection();
            c.setConnectTimeout(1000);
            c.setReadTimeout(1000);
            int code = c.getResponseCode();
            c.disconnect();
            android.util.Log.i("MyCast", "backendReady -> " + code);
            return code == 200;
        } catch (IOException e) {
            android.util.Log.e("MyCast", "backendReady fail: " + e);
            return false;
        }
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        if (backend != null) backend.destroy(); // 关停内嵌服务端
    }
}
