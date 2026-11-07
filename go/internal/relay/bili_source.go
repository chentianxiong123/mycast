package relay

import (
	"context"
	"errors"

	"mycast/internal/medialib"
)

// BiliSource 错误。
var (
	errBiliNotReady = errors.New("bili: 客户端未就绪")
	errBiliNoKey    = errors.New("bili: 缺 aid/cid")
)

// BiliSource B站播放源：实现 medialib.Source。
// 收敛 B站「拿流（playurl）→ 投屏（startRelayByCID，含 URL 到期续期）」为一个源。
//
// 三组件解耦步骤4（见 docs/2026-11-07-三组件解耦-媒体库归一化架构.md）。
type BiliSource struct {
	Rl    *Relay
	Aid   string
	Cid   string
	Title string
}

// Name 源名。
func (b *BiliSource) Name() string { return "bili" }

// Resolve 拿可播放流：playurl → DASH/mp4 流（含 B站防盗链头）。
func (b *BiliSource) Resolve(ctx context.Context) (*medialib.Stream, error) {
	if b.Rl == nil || b.Rl.bili == nil {
		return nil, errBiliNotReady
	}
	if b.Aid == "" || b.Cid == "" {
		return nil, errBiliNoKey
	}
	st, err := b.Rl.bili.PlayURL(b.Aid, b.Cid, 80)
	if err != nil {
		return nil, err
	}
	return &medialib.Stream{
		URL:     st.URL,
		Mime:    st.MimeType,
		Title:   b.Title,
		Headers: map[string]string{"Referer": "https://www.bilibili.com/", "UA": b.Rl.bili.UA},
	}, nil
}

// Cast 投屏：走 startRelayByCID（已含 URL 到期续期，治 30 分钟卡顿）。
func (b *BiliSource) Cast(ctx context.Context) error {
	if b.Rl == nil {
		return errBiliNotReady
	}
	b.Rl.startRelayByCID(b.Aid, b.Cid)
	return nil
}
