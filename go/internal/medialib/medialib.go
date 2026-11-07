// Package medialib 媒体库组件：归一化播放源。
//
// 目标：各种播放源（B站/盗版站/本地文件）→ 统一抽象 → 投屏。
// 真正播放/投屏只走这里，不再散调 relay/bili/media。
//
// 边界原则（见 docs/2026-11-07-三组件解耦-媒体库归一化架构.md）：
//   - medialib 是接口定义层（Source/Stream），不依赖实现方（relay/bili）
//   - relay/bili 各自实现 Source（盗版站源/B站源），server 拿 Source 调
//   - search/sniff 保持叶子，不依赖媒体库
package medialib

import (
	"context"
	"errors"
	"strings"
)

// Stream 一个可播放的流（归一化：无论 B站/盗版/本地，都是这个形状）。
type Stream struct {
	URL     string            // 可播放 URL（电视能拉的）
	Mime    string            // m3u8 / mp4 / dash
	Headers map[string]string // 防盗链头（Referer/UA 透传）
	Renew   func() error      // URL 到期续期（B站 deadline→403）；nil=不续期
	Title   string            // 标题（投屏显示）
}

// Source 归一化播放源。各实现（B站源/盗版站源/本地源）统一此接口。
type Source interface {
	// Name 源名（"bili" / "pirate" / "local"）。
	Name() string
	// Resolve 拿可播放流（含防盗链头/续期能力）。
	Resolve(ctx context.Context) (*Stream, error)
	// Cast 投屏到电视（电视选择由实现内部管理，如 relay.SelectTV）。
	Cast(ctx context.Context) error
}

// ErrNotCastable 明确不可播（真验证码/DRM 等原理性边界）。
var ErrNotCastable = errors.New("medialib: 源不可播放（验证码/DRM 边界）")

// ErrResolve 拿流失败。
var ErrResolve = errors.New("medialib: 拿流失败")

// MimeOf 从 URL 猜 mime（m3u8/mp4/dash）。
func MimeOf(rawURL string) string {
	switch {
	case strings.Contains(rawURL, ".m3u8"):
		return "m3u8"
	case strings.Contains(rawURL, ".mp4"):
		return "mp4"
	case strings.Contains(rawURL, "upgcxcode") || strings.Contains(rawURL, "bilibili"):
		return "dash"
	default:
		return "mp4"
	}
}
