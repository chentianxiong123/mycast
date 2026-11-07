package sniff

import "testing"

func TestExtractPoster(t *testing.T) {
	e := New(Default())
	base := "https://www.vipjuji.com/voddetail/22548.html"
	cases := []struct {
		name, html, want string
	}{
		{
			"og:image meta",
			`<html><head><meta property="og:image" content="https://cdn.site.com/cover/1.jpg"></head><body>x</body></html>`,
			"https://cdn.site.com/cover/1.jpg",
		},
		{
			"img class=cover",
			`<html><body><img class="lazyload cover" data-src="/uploads/pic/abc.jpg"></body></html>`,
			"https://www.vipjuji.com/uploads/pic/abc.jpg",
		},
		{
			"img poster attr",
			`<html><body><img poster="https://cdn.site.com/p.png"></body></html>`,
			"https://cdn.site.com/p.png",
		},
		{
			"无封面",
			`<html><body><p>no image</p></body></html>`,
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := e.ExtractPoster(c.html, base)
			if got != c.want {
				t.Fatalf("ExtractPoster=%q want %q", got, c.want)
			}
		})
	}
}

func TestExtractPosterPrefersOgImage(t *testing.T) {
	e := New(Default())
	html := `<html><head><meta property="og:image" content="https://cdn/og.jpg"></head>` +
		`<body><img class="cover" src="/local.jpg"></body></html>`
	got := e.ExtractPoster(html, "https://site.com/detail/1.html")
	if got != "https://cdn/og.jpg" {
		t.Fatalf("og:image 应优先, got %q", got)
	}
}
