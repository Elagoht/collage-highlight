package highlight_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	highlight "github.com/Elagoht/collage-highlight"
	"github.com/Elagoht/collage/pkg/collage"
)

func newSite(opts highlight.Options, pages map[string]string) (*collage.App, error) {
	templates := fstest.MapFS{}
	for name, body := range pages {
		templates["t/"+name+".html"] = &fstest.MapFile{Data: []byte(body)}
	}
	app, err := collage.New(&collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: templates, Root: "t"},
		Plugins:  []collage.Plugin{highlight.New(opts)},
	})
	if err != nil {
		return nil, err
	}
	for name := range pages {
		path := "/" + name
		if name == "index" {
			path = "/"
		}
		page := collage.NewPage(name).
			WithContent(collage.NewFragment(name, name+".html").WithData(collage.Value(map[string]string{"Code": `if a < b { fmt.Println("x") }`})).Build()).
			WithPath("en", path).
			Build()
		if err := app.RegisterPage(page); err != nil {
			return nil, err
		}
	}
	return app, nil
}

func site(t *testing.T, opts highlight.Options, pages map[string]string) *collage.App {
	t.Helper()
	app, err := newSite(opts, pages)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func get(app *collage.App, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var styleLink = regexp.MustCompile(`<link rel="stylesheet" href="(/_highlight/style[^"]*\.css)">`)

func TestTemplateFunction(t *testing.T) {
	app := site(t, highlight.Options{}, map[string]string{
		"index": `<html><head>{{hoist "head"}}</head><body>{{highlight .Code "go"}}{{highlight "plain <text>" "no-such-language"}}</body></html>`,
	})
	body := get(app, "/").Body.String()
	for _, want := range []string{
		`<pre class="chroma"><code>`,
		`<span class="k">if</span>`,
		`<span class="p">&lt;</span>`,
		`<span class="s">&#34;x&#34;</span>`,
		`plain &lt;text&gt;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s\n%s", want, body)
		}
	}
	links := styleLink.FindAllStringSubmatch(body, -1)
	if len(links) != 1 {
		t.Fatalf("%d stylesheet links in\n%s", len(links), body)
	}
	if !regexp.MustCompile(`^/_highlight/style\.[0-9a-f]{16}\.css$`).MatchString(links[0][1]) {
		t.Errorf("the link is not the content-addressed stylesheet: %s", links[0][1])
	}
	if rec := get(app, links[0][1]); rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("GET %s = %d, Cache-Control %q", links[0][1], rec.Code, rec.Header().Get("Cache-Control"))
	}
}

const markdownPage = `<!DOCTYPE html>
<html><head><title>T</title></head><body>
<P CLASS=kept>Around the code, nothing changes: &amp; <br/></P>
<pre><code class="language-go">func main() {
	if a &lt; b {}
}
</code></pre>
<pre><code class="language-no-such-language">left &lt;alone&gt;</code></pre>
<pre><code>no language</code></pre>
<pre><code class="language-go"><span>marked up</span></code></pre>
<script>var s = '<pre><code class="language-go">x</code></pre>';</script>
</body></html>`

func TestAutoColoursCodeBlocks(t *testing.T) {
	app := site(t, highlight.Options{}, map[string]string{"index": markdownPage})
	body := get(app, "/").Body.String()
	for _, want := range []string{
		`<span class="kd">func</span>`,
		`<span class="p">&lt;</span>`,
		`<P CLASS=kept>Around the code, nothing changes: &amp; <br/></P>`,
		`<pre><code class="language-no-such-language">left &lt;alone&gt;</code></pre>`,
		`<pre><code>no language</code></pre>`,
		`<pre><code class="language-go"><span>marked up</span></code></pre>`,
		`<script>var s = '<pre><code class="language-go">x</code></pre>';</script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s\n%s", want, body)
		}
	}
	if n := strings.Count(body, `class="chroma"`); n != 1 {
		t.Errorf("%d blocks coloured, want 1\n%s", n, body)
	}
	links := styleLink.FindAllStringSubmatch(body, -1)
	if len(links) != 1 || !strings.Contains(body, links[0][0]+"</head>") {
		t.Fatalf("the stylesheet is not linked at the end of the head\n%s", body)
	}
	if !regexp.MustCompile(`^/_highlight/style\.[0-9a-f]{16}\.css$`).MatchString(links[0][1]) {
		t.Errorf("the link is not the content-addressed stylesheet: %s", links[0][1])
	}
}

// A page coloured by {{highlight}} and by the pass links the stylesheet once.
func TestOneLink(t *testing.T) {
	app := site(t, highlight.Options{}, map[string]string{
		"index": `<html><head>{{hoist "head"}}</head><body>{{highlight .Code "go"}}<pre><code class="language-go">x := 1</code></pre></body></html>`,
	})
	body := get(app, "/").Body.String()
	if n := strings.Count(body, `class="chroma"`); n != 2 {
		t.Errorf("%d blocks coloured, want 2", n)
	}
	if n := len(styleLink.FindAllString(body, -1)); n != 1 {
		t.Errorf("%d stylesheet links, want 1\n%s", n, body)
	}
}

// The link lands where the layout put {{hoist "head"}}, not merely before
// </head>: ahead of the site's own stylesheet, which can then override it.
func TestLinkedWhereTheLayoutHoists(t *testing.T) {
	app := site(t, highlight.Options{}, map[string]string{
		"index": `<html><head><title>T</title>{{hoist "head"}}<link rel="stylesheet" href="/site.css"></head><body><pre><code class="language-go">x := 1</code></pre></body></html>`,
	})
	body := get(app, "/").Body.String()
	links := styleLink.FindAllStringSubmatch(body, -1)
	if len(links) != 1 || !strings.Contains(body, `<title>T</title>`+links[0][0]+`<link rel="stylesheet" href="/site.css">`) {
		t.Fatalf("the stylesheet is not linked at the layout's hoist\n%s", body)
	}
	if n := strings.Count(body, `class="chroma"`); n != 1 {
		t.Errorf("%d blocks coloured, want 1\n%s", n, body)
	}
}

// A block before the hoist — a <pre> written in the head — is still replaced
// whole, though the link moved everything after it but not it.
func TestBlockBeforeTheHoist(t *testing.T) {
	app := site(t, highlight.Options{}, map[string]string{
		"index": `<html><head><pre><code class="language-go">x := 1</code></pre>{{hoist "head"}}</head><body><pre><code class="language-go">y := 2</code></pre></body></html>`,
	})
	body := get(app, "/").Body.String()
	if n := strings.Count(body, `class="chroma"`); n != 2 || strings.Contains(body, "language-go") {
		t.Errorf("%d blocks coloured, want both\n%s", n, body)
	}
	if n := len(styleLink.FindAllString(body, -1)); n != 1 || !strings.HasSuffix(strings.SplitN(body, "</head>", 2)[0], ">") {
		t.Errorf("%d stylesheet links\n%s", n, body)
	}
	if !strings.HasPrefix(body, `<html><head><pre class="chroma">`) {
		t.Errorf("the head's block was not replaced in place\n%s", body)
	}
}

// A page without code, or with Auto off, is served exactly as rendered.
func TestUntouched(t *testing.T) {
	plain := `<html><head></head><body><p>No code</p><code class="language-go">inline</code></body></html>`
	app := site(t, highlight.Options{}, map[string]string{"index": plain})
	if body := get(app, "/").Body.String(); body != plain {
		t.Errorf("a page without blocks changed:\n%s", body)
	}
	off := false
	app = site(t, highlight.Options{Auto: &off}, map[string]string{"index": markdownPage})
	if body := get(app, "/").Body.String(); strings.Contains(body, "chroma") || strings.Contains(body, "_highlight") {
		t.Errorf("Auto off coloured the page:\n%s", body)
	}
}

func TestStylesheet(t *testing.T) {
	app := site(t, highlight.Options{Light: "GitHub", Dark: "monokai"}, map[string]string{"index": "x"})
	rec := get(app, highlight.StylePath)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("GET %s = %d %q", highlight.StylePath, rec.Code, rec.Header().Get("Content-Type"))
	}
	css := rec.Body.String()
	for _, want := range []string{"@media (prefers-color-scheme: light) {", "@media (prefers-color-scheme: dark) {", ".chroma .kd", "background-color"} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet lacks %s\n%s", want, css)
		}
	}

	app = site(t, highlight.Options{Dark: "-", NoBackground: true}, map[string]string{"index": "x"})
	css = get(app, highlight.StylePath).Body.String()
	if strings.Contains(css, "@media") {
		t.Errorf("Dark \"-\" still has a dark theme:\n%s", css)
	}
	for _, line := range strings.Split(css, "\n") {
		if (strings.Contains(line, ".bg {") || strings.Contains(line, ".chroma {")) && strings.Contains(line, "background-color") {
			t.Errorf("NoBackground kept the block's background: %s", line)
		}
	}
}

// A static build writes the stylesheet the pages link.
func TestStaticBuild(t *testing.T) {
	app := site(t, highlight.Options{}, map[string]string{"index": markdownPage})
	out := t.TempDir()
	b, err := collage.NewBuilder(app, collage.BuildOptions{OutDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	m := styleLink.FindSubmatch(page)
	if m == nil {
		t.Fatalf("the built page links no stylesheet:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(string(m[1])))); err != nil {
		t.Errorf("the build did not write %s", m[1])
	}
}

func TestUnknownStyleStopsNew(t *testing.T) {
	for _, opts := range []highlight.Options{{Light: "no-such-style"}, {Dark: "nope"}} {
		if _, err := newSite(opts, map[string]string{"index": "x"}); err == nil {
			t.Errorf("%+v: collage.New succeeded", opts)
		}
	}
}

// Registered after New, the plugin never gets to add {{highlight}}, and says so.
func TestRegisteredLate(t *testing.T) {
	app, err := collage.New(&collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{"t/p.html": {Data: []byte("x")}}, Root: "t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterPlugin(highlight.New(highlight.Options{})); err == nil {
		t.Error("RegisterPlugin accepted a plugin with a template function after New")
	}
}
