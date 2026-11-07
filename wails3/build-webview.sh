#!/bin/bash
# 生成 vendor-wails（wails3 源码 + TLS IGNORE patch）：渲染窗口 https 自签回传 outerHTML
# 桌面 wails3 壳 build 前先跑：./build-webview.sh
set -e
cd "$(dirname "$0")"
MOD=$(go env GOMODCACHE)/github.com/wailsapp/wails/v3@v3.1.0
[ -d "$MOD" ] || go mod download github.com/wailsapp/wails/v3@v3.1.0
rm -rf vendor-wails
cp -r "$MOD" vendor-wails
chmod -R u+w vendor-wails
# patch：渲染窗口信任自签证书（TLS IGNORE）——so.com https 页 fetch https://127.0.0.1:8443 回传
python3 - <<'PY'
p = "vendor-wails/pkg/application/linux_cgo.go"
s = open(p).read()
if "set_tls_errors_policy" not in s:
    old = "C.webkit_web_view_set_settings(C.webkit_web_view((*C.GtkWidget)(webView)), settings)"
    new = ("sess := C.webkit_web_view_get_network_session(C.webkit_web_view((*C.GtkWidget)(webView)))\n"
           "\tC.webkit_network_session_set_tls_errors_policy(sess, C.WEBKIT_TLS_ERRORS_POLICY_IGNORE)\n"
           "\t" + old)
    s = s.replace(old, new)
    open(p, "w").write(s)
    print("patched: TLS IGNORE（渲染窗口 https 自签信任）")
else:
    print("patched already")
PY
echo "vendor-wails 就绪"
