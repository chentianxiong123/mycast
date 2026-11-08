package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"mycast/internal/search"
	"mycast/internal/sniff"
)

// fakeSearch 模拟搜索（不发网络）。
type fakeSearch struct{}

func (fakeSearch) Search(_ context.Context, q string) ([]search.Result, error) {
	return []search.Result{
		{Engine: "bing", Title: "站A", URL: "https://a.com/detail/1.html"},
		{Engine: "so", Title: "站B", URL: "https://b.com/vod/2.html"},
	}, nil
}

// fakeSniff 模拟嗅探 + 拿流。
type fakeSniff struct{}

func (fakeSniff) ExtractURL(u string) ([]sniff.Episode, error) {
	return []sniff.Episode{
		{Index: 1, Title: "1", URL: u + "?ep=1"},
		{Index: 2, Title: "2", URL: u + "?ep=2"},
	}, nil
}
func (fakeSniff) ResolveStream(u string) (string, string, error) {
	return "https://cdn.example.com/index.m3u8", "application/vnd.apple.mpegurl", nil
}

// initSession 走完整 initialize 握手，返回带 session 的 http.Handler 包装（真库要求）。
func initSession(t *testing.T, h http.Handler) http.Handler {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	sid := rec.Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("initialize 未下发 session")
	}
	// 返回一个包装 handler：自动附上 session header（模拟同一客户端后续请求）
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Mcp-Session-Id", sid)
		h.ServeHTTP(w, r)
	})
}

// callTool 走真库的 HTTP 层调一个工具，返回 text 内容。
func callTool(t *testing.T, h http.Handler, name string, args map[string]any) (string, bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var out struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应非 JSON: %v body=%s", err, rec.Body.String())
	}
	if len(out.Result.Content) == 0 {
		return "", false
	}
	return out.Result.Content[0].Text, true
}

func TestSearchPirate(t *testing.T) {
	srv := New(fakeSearch{}, fakeSniff{}, nil)
	text, ok := callTool(t, initSession(t, srv.Handler()), "search_pirate", map[string]any{"query": "繁花"})
	if !ok || !strings.Contains(text, "站A") {
		t.Fatalf("search_pirate 失败: %s", text)
	}
}

func TestSearchMissingQuery(t *testing.T) {
	srv := New(fakeSearch{}, fakeSniff{}, nil)
	text, _ := callTool(t, initSession(t, srv.Handler()), "search_pirate", map[string]any{})
	if !strings.Contains(text, "缺少") {
		t.Fatalf("期望缺 query 报错，得到 %s", text)
	}
}

func TestSniffEpisodes(t *testing.T) {
	srv := New(fakeSearch{}, fakeSniff{}, nil)
	text, ok := callTool(t, initSession(t, srv.Handler()), "sniff_episodes", map[string]any{"url": "https://a.com/detail/1.html"})
	if !ok || !strings.Contains(text, "\"count\":2") {
		t.Fatalf("sniff_episodes 失败: %s", text)
	}
}

func TestCastDirectStreamURL(t *testing.T) {
	// 直链 m3u8：透传（Cast nil 安全跳过）
	srv := New(fakeSearch{}, fakeSniff{}, nil)
	text, ok := callTool(t, initSession(t, srv.Handler()), "cast_to_tv", map[string]any{"url": "https://cdn.x.com/a/b/index.m3u8"})
	if !ok || !strings.Contains(text, "index.m3u8") {
		t.Fatalf("直链未透传: %s", text)
	}
}

func TestCastResolvesPlayPage(t *testing.T) {
	srv := New(fakeSearch{}, fakeSniff{}, nil)
	text, ok := callTool(t, initSession(t, srv.Handler()), "cast_to_tv", map[string]any{"url": "https://x.com/vodplay/1.html"})
	if !ok || !strings.Contains(text, "index.m3u8") {
		t.Fatalf("播放页未解析: %s", text)
	}
}

func TestCastMissingURL(t *testing.T) {
	srv := New(fakeSearch{}, fakeSniff{}, nil)
	text, _ := callTool(t, initSession(t, srv.Handler()), "cast_to_tv", map[string]any{})
	if !strings.Contains(text, "缺少") {
		t.Fatalf("期望缺 url 报错，得到 %s", text)
	}
}

func TestToolsList(t *testing.T) {
	srv := New(fakeSearch{}, fakeSniff{}, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	initSession(t, srv.Handler()).ServeHTTP(rec, req)

	var out struct {
		Result struct {
			Tools []mcp.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("tools/list 响应非 JSON: %v", err)
	}
	if len(out.Result.Tools) != 3 {
		t.Fatalf("期望 3 工具，得到 %d", len(out.Result.Tools))
	}
}

func TestInitialize(t *testing.T) {
	srv := New(fakeSearch{}, fakeSniff{}, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	initSession(t, srv.Handler()).ServeHTTP(rec, req)

	if rec.Header().Get("Mcp-Session-Id") == "" {
		t.Fatal("initialize 未下发 Mcp-Session-Id")
	}
	if !strings.Contains(rec.Body.String(), "2025-03-26") {
		t.Fatalf("protocolVersion 错误: %s", rec.Body.String())
	}
}
