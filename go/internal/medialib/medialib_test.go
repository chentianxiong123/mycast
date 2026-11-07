package medialib

import (
	"context"
	"errors"
	"testing"
)

// fakeSource 测试用：实现 Source 接口，验证接口契约。
type fakeSource struct{ name string }

func (f fakeSource) Name() string { return f.name }
func (f fakeSource) Resolve(context.Context) (*Stream, error) {
	return &Stream{URL: "https://x/y.m3u8", Mime: "m3u8", Title: "测试剧"}, nil
}
func (f fakeSource) Cast(context.Context) error { return nil }

func TestSourceInterface(t *testing.T) {
	// 编译期断言：fakeSource 满足 Source
	var _ Source = fakeSource{name: "test"}

	src := fakeSource{name: "test"}
	if src.Name() != "test" {
		t.Fatalf("Name 不对: %q", src.Name())
	}
	st, err := src.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if st.URL != "https://x/y.m3u8" || st.Mime != "m3u8" {
		t.Fatalf("Stream 不对: %+v", st)
	}
	if err := src.Cast(context.Background()); err != nil {
		t.Fatalf("Cast: %v", err)
	}
}

func TestMimeOf(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{"https://a.com/1/index.m3u8", "m3u8"},
		{"https://a.com/v.mp4", "mp4"},
		{"https://upgcxcode.com/1/2.m4s", "dash"},
		{"https://x/y", "mp4"}, // 默认
	}
	for _, c := range cases {
		if got := MimeOf(c.url); got != c.want {
			t.Errorf("MimeOf(%q)=%q want %q", c.url, got, c.want)
		}
	}
}

func TestErrors(t *testing.T) {
	if !errors.Is(ErrNotCastable, ErrNotCastable) {
		t.Fatal("ErrNotCastable 应能 errors.Is 匹配")
	}
	if ErrNotCastable == ErrResolve {
		t.Fatal("错误应不同")
	}
}

func TestStreamRenew(t *testing.T) {
	// Renew 为 nil 时不应 panic（表示不续期）
	st := &Stream{URL: "x", Mime: "mp4"}
	if st.Renew != nil {
		t.Fatal("空 Renew 应为 nil")
	}
}
