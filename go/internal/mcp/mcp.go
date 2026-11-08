// Package mcp 为 mycast 提供 MCP（Model Context Protocol）Streamable HTTP 端点。
//
// 用官方 Go MCP 库 github.com/mark3labs/mcp-go 实现（不再手写协议），
// 原生支持 Streamable HTTP，直接当 http.Handler 挂进 mycast 单二进制。
//
// 暴露给 AI 的工具（老人一句话投屏的最后一环）：
//   - search_pirate  搜盗版站（复用 search.Aggregator，bing/360/百度 自聚合）
//   - sniff_episodes 详情页 URL → 集数列表（复用 sniff.Extractor.ExtractURL）
//   - cast_to_tv     播放页 URL → 拿流(m3u8/mp4) → 投屏（复用 relay.CastStream）
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	search "github.com/chentianxiong123/search-service"
	"mycast/internal/sniff"
)

// Searcher 搜索能力（*search.Aggregator 满足）。
type Searcher interface {
	Search(ctx context.Context, query string, opts search.Options) ([]search.Result, error)
}

// Sniffer 集数提取 + 拿流（*sniff.Extractor 满足）。
type Sniffer interface {
	ExtractURL(rawURL string) ([]sniff.Episode, error)
	ResolveStream(playURL string) (streamURL, mime string, err error)
}

// Caster 投屏能力（*relay.Relay 满足，用接口避免 mcp 依赖投屏包）。
type Caster interface {
	CastStream(streamURL, referer string)
}

// Server MCP 端点：持有 mycast 的搜索/嗅探/投屏依赖（接口，便于测试注入）。
type Server struct {
	Search Searcher
	Sniff  Sniffer
	Cast   Caster

	mcpServer *server.MCPServer
	httpSrv   *server.StreamableHTTPServer
}

// New 创建 MCP 服务（mcp-go 官方库）。
func New(s Searcher, sn Sniffer, c Caster) *Server {
	srv := &Server{Search: s, Sniff: sn, Cast: c}

	ms := server.NewMCPServer("mycast", "0.1.0")
	ms.AddTool(mcp.NewTool("search_pirate",
		mcp.WithDescription("搜索盗版影视站（bing/360/百度 三引擎聚合）。输入剧名/关键词，返回站名+详情页 URL。"),
		mcp.WithString("query", mcp.Required(), mcp.Description("搜索关键词，如：繁花 在线观看")),
	), srv.handleSearch)
	ms.AddTool(mcp.NewTool("sniff_episodes",
		mcp.WithDescription("从盗版站详情页 URL 提取全部集数。输入详情页链接，返回第N集+播放页 URL 列表。"),
		mcp.WithString("url", mcp.Required(), mcp.Description("盗版站详情页 URL，如 https://xxx.com/voddetail/22548.html")),
	), srv.handleSniff)
	ms.AddTool(mcp.NewTool("cast_to_tv",
		mcp.WithDescription("拿流并投屏到电视。输入某一集的播放页 URL（或直接 m3u8/mp4），自动提取流地址并投屏。"),
		mcp.WithString("url", mcp.Required(), mcp.Description("播放页 URL 或直链 m3u8/mp4")),
		mcp.WithString("title", mcp.Description("可选的播放标题")),
	), srv.handleCast)

	srv.mcpServer = ms
	srv.httpSrv = server.NewStreamableHTTPServer(ms)
	return srv
}

// Handler 返回 /mcp 的 http.Handler（Streamable HTTP 传输）。
func (s *Server) Handler() http.Handler {
	return s.httpSrv
}

// handleSearch 搜盗版站。
func (s *Server) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	qs, _ := args["query"].(string)
	if qs == "" {
		return mcp.NewToolResultError("缺少关键词 query"), nil
	}
	rs, err := s.Search.Search(ctx, qs, search.Options{})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return toolResult(map[string]any{"query": qs, "results": rs})
}

// handleSniff 详情页 → 集数。
func (s *Server) handleSniff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	u, _ := args["url"].(string)
	if u == "" {
		return mcp.NewToolResultError("缺少详情页 URL"), nil
	}
	eps, err := s.Sniff.ExtractURL(u)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return toolResult(map[string]any{"url": u, "count": len(eps), "episodes": eps})
}

// handleCast 播放页/直链 → 拿流 → 投屏。
func (s *Server) handleCast(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	u, _ := args["url"].(string)
	title, _ := args["title"].(string)
	if u == "" {
		return mcp.NewToolResultError("缺少播放 URL"), nil
	}
	// 直链 m3u8/mp4：直接投；否则先解析播放页拿流。
	streamURL, mime := u, ""
	if !isStreamURL(u) {
		var err error
		streamURL, mime, err = s.Sniff.ResolveStream(u)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}
	ref := refererOf(u)
	if s.Cast != nil {
		s.Cast.CastStream(streamURL, ref)
	}
	return toolResult(map[string]any{"ok": true, "stream_url": streamURL, "mime": mime, "referer": ref, "title": title})
}

// toolResult 序列化业务结果为 text 内容。
func toolResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("序列化失败: " + err.Error()), nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{Type: "text", Text: string(b)}},
	}, nil
}

// isStreamURL 判断是否已是 m3u8/mp4 直链。
func isStreamURL(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, ".m3u8") || strings.Contains(l, ".mp4")
}

// refererOf 防盗链 referer = 来源页域名（透传，不能猜）。
func refererOf(u string) string {
	if i := strings.Index(u, "://"); i > 0 {
		rest := u[i+3:]
		if j := strings.Index(rest, "/"); j > 0 {
			return u[:i+3] + rest[:j] + "/"
		}
	}
	return ""
}
