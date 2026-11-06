package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mycast/internal/search"
	"mycast/internal/sniff"
)

// newTestSrv 用真实空依赖建 MCP 服务（不触发网络：只测协议层路径）。
func newTestSrv() *Server {
	return New(&search.Aggregator{}, &sniff.Extractor{}, nil)
}

// post 发一个 MCP JSON-RPC 请求，返回响应。
func post(t *testing.T, srv *Server, body string) *jsonRPC {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	rec := httptest.NewRecorder()
	srv.handleMCP(rec, req)
	var out jsonRPC
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应非 JSON: %v body=%s", err, rec.Body.String())
	}
	return &out
}

func TestInitializeIssuesSession(t *testing.T) {
	srv := newTestSrv()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleMCP(rec, req)

	if rec.Header().Get("Mcp-Session-Id") == "" {
		t.Fatal("initialize 未下发 Mcp-Session-Id")
	}
	var out jsonRPC
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	res, _ := out.Result.(map[string]any)
	if res["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion=%v", res["protocolVersion"])
	}
}

func TestToolsList(t *testing.T) {
	srv := newTestSrv()
	out := post(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if out.Error != nil {
		t.Fatalf("tools/list error: %v", out.Error)
	}
	res := out.Result.(map[string]any)
	tools, _ := res["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("期望 3 个工具，得到 %d", len(tools))
	}
}

func TestMethodNotFound(t *testing.T) {
	srv := newTestSrv()
	out := post(t, srv, `{"jsonrpc":"2.0","id":9,"method":"nope","params":{}}`)
	if out.Error == nil || out.Error.Code != -32601 {
		t.Fatalf("期望 -32601，得到 %+v", out.Error)
	}
}

func TestToolNotFound(t *testing.T) {
	srv := newTestSrv()
	out := post(t, srv, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if out.Error == nil || out.Error.Code != -32602 {
		t.Fatalf("期望 -32602，得到 %+v", out.Error)
	}
}

func TestCastRequiresURL(t *testing.T) {
	srv := newTestSrv()
	out := post(t, srv, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"cast_to_tv","arguments":{}}}`)
	if !strings.Contains(toolText(out), "缺少") {
		t.Fatalf("期望缺 URL 报错，得到 %s", toolText(out))
	}
}

func TestCastDirectStreamURL(t *testing.T) {
	// 直链 m3u8：不走 ResolveStream，直接透传（Relay nil 时安全跳过投屏）
	srv := newTestSrv()
	out := post(t, srv, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"cast_to_tv","arguments":{"url":"https://cdn.x.com/a/b/index.m3u8"}}}`)
	text := toolText(out)
	if !strings.Contains(text, "index.m3u8") {
		t.Fatalf("直链未透传，得到 %s", text)
	}
}

func TestCastResolvesPlayPage(t *testing.T) {
	// 播放页 URL：调 ResolveStream（fakeSniff 返回固定流）
	srv := New(&search.Aggregator{}, fakeSniff{}, nil)
	out := post(t, srv, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"cast_to_tv","arguments":{"url":"https://x.com/vodplay/1.html"}}}`)
	text := toolText(out)
	if !strings.Contains(text, "index.m3u8") {
		t.Fatalf("播放页未解析，得到 %s", text)
	}
}

func TestSearchMissingQuery(t *testing.T) {
	srv := newTestSrv()
	out := post(t, srv, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"search_pirate","arguments":{}}}`)
	if !strings.Contains(toolText(out), "缺少") {
		t.Fatalf("期望缺 query 报错，得到 %s", toolText(out))
	}
}

func TestSniffMissingURL(t *testing.T) {
	srv := newTestSrv()
	out := post(t, srv, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"sniff_episodes","arguments":{}}}`)
	if !strings.Contains(toolText(out), "缺少") {
		t.Fatalf("期望缺 url 报错，得到 %s", toolText(out))
	}
}

// fakeSniff 满足 Sniffer 接口（ResolveStream 返回固定流）。
type fakeSniff struct{}

func (fakeSniff) ResolveStream(u string) (string, string, error) {
	return "https://cdn.example.com/index.m3u8", "application/vnd.apple.mpegurl", nil
}

func (fakeSniff) ExtractURL(u string) ([]sniff.Episode, error) {
	return []sniff.Episode{{Index: 1, Title: "1", URL: u + "?ep=1"}}, nil
}

// toolText 取 tools/call 结果里的 text 内容。
func toolText(out *jsonRPC) string {
	if out.Result == nil {
		return ""
	}
	res, _ := out.Result.(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	m, _ := content[0].(map[string]any)
	s, _ := m["text"].(string)
	return s
}
