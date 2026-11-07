package relay

import (
	"context"
	"errors"
	"net/url"

	"mycast/internal/medialib"
	"mycast/internal/sniff"
)

// PirateSource 盗版站播放源：实现 medialib.Source。
// 收敛「拿流（sniff.ResolveStream）→ 投屏（relay.CastStream）」两步为一个源，
// 让 server 只调 medialib.Source.Cast，不再散调 sniff+relay。
//
// 三组件解耦步骤3（见 docs/2026-11-07-三组件解耦-媒体库归一化架构.md）。
type PirateSource struct {
	Rl      *Relay
	Extract *sniff.Extractor
	PlayURL string // 播放页 URL（Resolve 时拿流）
	Title   string
}

// Name 源名。
func (p *PirateSource) Name() string { return "pirate" }

// Resolve 拿可播放流：解析播放页 → m3u8/mp4（含防盗链头）。
func (p *PirateSource) Resolve(_ context.Context) (*medialib.Stream, error) {
	if p.Extract == nil {
		return nil, errors.New("pirate: Extract 未设置")
	}
	if p.PlayURL == "" {
		return nil, errors.New("pirate: 空播放页 URL")
	}
	streamURL, mime, err := p.Extract.ResolveStream(p.PlayURL)
	if err != nil {
		return nil, err
	}
	return &medialib.Stream{
		URL:     streamURL,
		Mime:    mime,
		Title:   p.Title,
		Headers: map[string]string{"Referer": originOf(p.PlayURL)},
	}, nil
}

// Cast 投屏：拿流后直接投给电视（电视选择由 relay 内部管理）。
func (p *PirateSource) Cast(ctx context.Context) error {
	st, err := p.Resolve(ctx)
	if err != nil {
		return err
	}
	p.Rl.CastStream(st.URL, st.Headers["Referer"])
	return nil
}

// CastWith 拿流+投屏，返回流信息（给 server 展示 url/mime/ref）。
func (p *PirateSource) CastWith(ctx context.Context) (*medialib.Stream, error) {
	st, err := p.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	p.Rl.CastStream(st.URL, st.Headers["Referer"])
	return st, nil
}

// originOf 取 URL 的 scheme://host（防盗链 Referer 用）。
func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
