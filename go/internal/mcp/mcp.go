// Package mcp 为 mycast 提供 MCP（Model Context Protocol）Streamable HTTP 端点。
//
// 模仿 searxng-mcp 的传输模式（initialize/tools/list/tools/call + Mcp-Session-Id），
// 但用 Go 标准库 net/http 重写，零外部依赖，内嵌进 mycast 单二进制。
//
// 暴露给 AI 的工具（老人一句话投屏的最后一环）：
//   - search_pirate  搜盗版站（复用 search.Aggregator，bing/360/百度 自聚合）
//   - sniff_episodes 详情页 URL → 集数列表（复用 sniff.Extractor.ExtractURL）
//   - cast_to_tv     播放页 URL → 拿流(m3u8/mp4) → 投屏（复用 relay.CastStream）
package mcp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"mycast/internal/search"
	"mycast/internal/sniff"
)

// ProtocolVersion MCP 协议版本（对齐 searxng-mcp）。
const ProtocolVersion = "2025-03-26"

// Server MCP 端点：持有 mycast 的搜索/嗅探/投屏依赖（接口，便于测试注入）。
type Server struct {
	Search Searcher
	Sniff  Sniffer
	Relay  Caster

	mu       sync.Mutex
	sessions map[string]time.Time // session_id → 创建时间
}

// Searcher 搜索能力（*search.Aggregator 满足）。
type Searcher interface {
	Search(ctx context.Context, query string) ([]search.Result, error)
}

// Sniffer 集数提取 + 拿流（*sniff.Extractor 满足）。
type Sniffer interface {
	ExtractURL(rawURL string) ([]sniff.Episode, error)
	ResolveStream(playURL string) (streamURL, mime string, err error)
}

// Caster 投屏能力（*relay.Relay 满足）。
type Caster interface {
	CastStream(streamURL, referer string)
}

// New 创建 MCP 服务。
func New(s Searcher, sn Sniffer, rl Caster) *Server {
	return &Server{
		Search:   s,
		Sniff:    sn,
		Relay:    rl,
		sessions: map[string]time.Time{},
	}
}

// tool 一个 MCP 工具定义。
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// tools 暴露给 AI 的工具清单。
var tools = []tool{
	{
		Name:        "search_pirate",
		Description: "搜索盗版影视站（bing/360/百度 三引擎聚合）。输入剧名/关键词，返回站名+详情页 URL。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "搜索关键词，如：繁花 在线观看"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "sniff_episodes",
		Description: "从盗版站详情页 URL 提取全部集数。输入详情页链接，返回第N集+播放页 URL 列表。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "盗版站详情页 URL，如 https://xxx.com/voddetail/22548.html"},
			},
			"required": []string{"url"},
		},
	},
	{
		Name:        "cast_to_tv",
		Description: "拿流并投屏到电视。输入某一集的播放页 URL（或直接 m3u8/mp4），自动提取流地址并投屏。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":   map[string]any{"type": "string", "description": "播放页 URL 或直链 m3u8/mp4"},
				"title": map[string]any{"type": "string", "description": "可选的播放标题", "default": ""},
			},
			"required": []string{"url"},
		},
	},
}

// Handler 返回 /mcp 的 http.Handler（Streamable HTTP 传输）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	return mux
}

// handleMCP MCP Streamable HTTP 端点。
// 只接受 POST（initialize/tools/list/tools/call），其余方法 405。
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "Method not allowed. Use POST.", http.StatusMethodNotAllowed)
		return
	}
	// CORS（供浏览器侧 MCP 客户端用）
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, MCP-Protocol-Version, Mcp-Session-Id, Mcp-Method, Mcp-Name, Mcp-Version, Authorization")

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeRPC(w, rpcError(nil, -32700, "Parse error: "+err.Error()))
		return
	}
	var req jsonRPC
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, rpcError(req.ID, -32700, "Parse error: invalid JSON"))
		return
	}

	sessionID := r.Header.Get("Mcp-Session-Id")
	resp := s.dispatch(r.Context(), &req, sessionID)

	// 初始化时下发新会话
	if req.Method == "initialize" {
		if id := resp.SessionID; id != "" {
			w.Header().Set("Mcp-Session-Id", id)
		}
	}
	writeRPC(w, resp)
}

// dispatch 分发 JSON-RPC 请求。
func (s *Server) dispatch(ctx context.Context, req *jsonRPC, sessionID string) *jsonRPC {
	switch req.Method {
	case "initialize":
		id := newSession()
		s.mu.Lock()
		s.sessions[id] = time.Now()
		s.mu.Unlock()
		return &jsonRPC{
			JSONRPC: "2.0", ID: req.ID, SessionID: id,
			Result: map[string]any{
				"protocolVersion": ProtocolVersion,
				"serverInfo":      map[string]any{"name": "mycast-mcp", "version": "0.1.0"},
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			},
		}
	case "notifications/initialized":
		return &jsonRPC{JSONRPC: "2.0", Result: map[string]any{}}
	case "ping":
		return &jsonRPC{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return &jsonRPC{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}}
	case "tools/call":
		return s.callTool(ctx, req)
	default:
		return rpcError(req.ID, -32601, "Method not found: "+req.Method)
	}
}

// callTool 执行 tools/call。
func (s *Server) callTool(ctx context.Context, req *jsonRPC) *jsonRPC {
	params, _ := req.Params.(map[string]any)
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)

	var out any
	var err error
	switch name {
	case "search_pirate":
		q, _ := args["query"].(string)
		out, err = s.doSearch(ctx, q)
	case "sniff_episodes":
		u, _ := args["url"].(string)
		out, err = s.doSniff(ctx, u)
	case "cast_to_tv":
		u, _ := args["url"].(string)
		title, _ := args["title"].(string)
		out, err = s.doCast(u, title)
	default:
		return rpcError(req.ID, -32602, "Tool not found: "+name)
	}
	if err != nil {
		return &jsonRPC{JSONRPC: "2.0", ID: req.ID,
			Result: map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}}}
	}
	b, _ := json.Marshal(out)
	return &jsonRPC{JSONRPC: "2.0", ID: req.ID,
		Result: map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}}
}

// doSearch 搜索盗版站。
func (s *Server) doSearch(ctx context.Context, q string) (any, error) {
	if strings.TrimSpace(q) == "" {
		return nil, errUser("缺少关键词 query")
	}
	rs, err := s.Search.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	return map[string]any{"query": q, "results": rs}, nil
}

// doSniff 详情页 → 集数。
func (s *Server) doSniff(ctx context.Context, u string) (any, error) {
	if strings.TrimSpace(u) == "" {
		return nil, errUser("缺少详情页 URL")
	}
	eps, err := s.Sniff.ExtractURL(u)
	if err != nil {
		return nil, err
	}
	return map[string]any{"url": u, "count": len(eps), "episodes": eps}, nil
}

// doCast 播放页/直链 → 拿流 → 投屏。
func (s *Server) doCast(u, title string) (any, error) {
	if strings.TrimSpace(u) == "" {
		return nil, errUser("缺少播放 URL")
	}
	// 直链 m3u8/mp4：直接投；否则先解析播放页拿流。
	streamURL, mime := u, ""
	if !isStreamURL(u) {
		var err error
		streamURL, mime, err = s.Sniff.ResolveStream(u)
		if err != nil {
			return nil, err
		}
	}
	ref := refererOf(u)
	if s.Relay != nil {
		s.Relay.CastStream(streamURL, ref)
	}
	return map[string]any{"ok": true, "stream_url": streamURL, "mime": mime, "referer": ref, "title": title}, nil
}

// isStreamURL 判断是否已是 m3u8/mp4 直链。
func isStreamURL(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, ".m3u8") || strings.Contains(l, ".mp4")
}

// refererOf 防盗链 referer = 来源页域名（透传，不能猜）。
func refererOf(u string) string {
	// 播放页 URL 的 scheme://host/ 作为 referer；直链则用其域名。
	if i := strings.Index(u, "://"); i > 0 {
		rest := u[i+3:]
		if j := strings.Index(rest, "/"); j > 0 {
			return u[:i+3] + rest[:j] + "/"
		}
	}
	return ""
}

// ── JSON-RPC 类型 ──

// jsonRPC 一个 MCP 请求（入参）或响应（出参）。
type jsonRPC struct {
	JSONRPC   string `json:"jsonrpc"`
	ID        any    `json:"id,omitempty"`
	Method    string `json:"method,omitempty"`
	Params    any    `json:"params,omitempty"`
	Result    any    `json:"result,omitempty"`
	Error     *rpcErr `json:"error,omitempty"`
	SessionID string `json:"-"` // 仅 initialize 响应下发
}

// rpcErr JSON-RPC 错误。
type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// errUser 业务错误（包装成 text 内容返回，不报 JSON-RPC error）。
type userErr struct{ msg string }

func (e userErr) Error() string { return e.msg }
func errUser(m string) error    { return userErr{m} }

func rpcError(id any, code int, msg string) *jsonRPC {
	return &jsonRPC{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg}}
}

func writeRPC(w http.ResponseWriter, r *jsonRPC) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(r)
}

// newSession 生成会话 ID（crypto/rand 16 字节 hex）。
func newSession() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0xf]
	}
	return string(out)
}
