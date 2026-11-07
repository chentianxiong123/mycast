package relay

import (
	"context"
	"testing"

	"mycast/internal/medialib"
)

// 编译期断言：PirateSource 满足 medialib.Source。
var _ medialib.Source = (*PirateSource)(nil)

func TestPirateSourceName(t *testing.T) {
	p := &PirateSource{}
	if p.Name() != "pirate" {
		t.Fatalf("Name 不对: %q", p.Name())
	}
}

func TestOriginOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.vipjuji.com/vodplay/1-1.html", "https://www.vipjuji.com"},
		{"http://x.com/a/b", "http://x.com"},
		{"bad", ""},
	}
	for _, c := range cases {
		if got := originOf(c.in); got != c.want {
			t.Errorf("originOf(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestPirateSourceCastNoPlayURL(t *testing.T) {
	// 无 PlayURL → Resolve 应报错（不 panic），Cast 返回错误。
	p := &PirateSource{Rl: &Relay{}}
	_, err := p.Resolve(context.Background())
	if err == nil {
		t.Fatal("空 PlayURL 应报错")
	}
}
