// mycast 安卓 cookie 获取（直接写 Kotlin，不 fork mygo、不等 mygo #2401）
// 验证"安卓能不能拿到 WebView cookie（含 httpOnly BDUSS）"：
//   安卓 WebView 渲染 baidu（存 cookie）→ CookieManager 取 baidu.com cookie（含 httpOnly BDUSS）→ 固化 cookies/baidu.json
// 用法：mycast 安卓 APK 里，mygo WebView 渲染 baidu 后，调 CookieFetch.saveBaiduCookies() 取 cookie 固化。

package dev.mycast.cookie

import android.content.Context
import android.webkit.CookieManager
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/**
 * 取 WebView cookie（含 httpOnly BDUSS）固化到 cookies/<engine>.json（httptool.CookieStore 格式）。
 * 安卓 WebView 渲染 baidu 后（CookieManager 存了 baidu.com cookie），调这拿全量 baidu cookie（含 httpOnly）。
 */
object CookieFetch {
    /** 取指定 engine（域名，如 "baidu"）的 cookie（含 httpOnly）固化到 cookies/<engine>.json。
     *  返回固化的 cookie 数量（0 = 没拿到，WebView 没渲染）。
     *  CookieManager.getInstance().getCookies(url) 拿该 URL 的 cookie（含 httpOnly，安卓 4.0+）。
     */
    fun saveEngineCookies(context: Context, engine: String, dir: String): Int {
        val cm = CookieManager.getInstance()
        // 取 baidu.com（engine="baidu" → baidu.com）的全量 cookie（含 httpOnly）
        val url = "https://www.${engine}.com/"
        val cookieStr = cm.getCookies(url) ?: ""  // 全量 baidu.com cookie（含 BDUSS httpOnly）
        if (cookieStr.isEmpty()) return 0

        // cookieStr 格式："BDUSS=xxx; BAIDUID=yyy; ..."（分号分隔）
        val arr = JSONArray()
        for (pair in cookieStr.split(";")) {
            if (pair.isBlank()) continue
            val idx = pair.indexOf('=')
            if (idx <= 0) continue
            val name = pair.substring(0, idx).trim()
            val value = pair.substring(idx + 1).trim()
            val obj = JSONObject()
            obj.put("domain", ".$engine.com")
            obj.put("name", name)
            obj.put("value", value)
            obj.put("path", "/")
            arr.put(obj)
        }

        // 固化到 cookies/<engine>.json（httptool.CookieStore 格式：{cookies:[{domain,name,value,path}]})
        val root = JSONObject()
        root.put("cookies", arr)
        File(dir).mkdirs()
        File(dir, "$engine.json").writeText(root.toString())
        return arr.length()
    }

    /** 清指定 engine 的 cookie（登录态失效时）。 */
    fun clearEngineCookies(engine: String) {
        val cm = CookieManager.getInstance()
        cm.removeSessionCookies()
        cm.flush()
    }
}
