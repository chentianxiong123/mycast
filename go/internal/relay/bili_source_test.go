package relay

import (
	"context"
	"testing"

	"mycast/internal/medialib"
)

// 编译期断言：BiliSource 满足 medialib.Source。
var _ medialib.Source = (*BiliSource)(nil)

func TestBiliSourceName(t *testing.T) {
	b := &BiliSource{}
	if b.Name() != "bili" {
		t.Fatalf("Name 不对: %q", b.Name())
	}
}

func TestBiliSourceResolveNoKey(t *testing.T) {
	// 无 aid/cid → 报错（不 panic）
	b := &BiliSource{Rl: &Relay{}}
	_, err := b.Resolve(context.Background())
	if err == nil {
		t.Fatal("缺 aid/cid 应报错")
	}
	if err != errBiliNoKey && err != errBiliNotReady {
		t.Fatalf("错误不对: %v", err)
	}
}

func TestBiliSourceResolveNotReady(t *testing.T) {
	// Rl 有但 bili 客户端未初始化 → 报错
	b := &BiliSource{Rl: &Relay{}, Aid: "1", Cid: "2"}
	_, err := b.Resolve(context.Background())
	if err == nil {
		t.Fatal("bili 客户端未就绪应报错")
	}
}
