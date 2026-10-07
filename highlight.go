// Package highlight is a collage plugin that colours code with chroma.
//
//	app, err := collage.New(&collage.Config{
//		Plugins: []collage.Plugin{highlight.New(highlight.Options{})},
//	})
//
// It colours code two ways. A template calls {{highlight .Code "go"}}; and every
// rendered page is read for the <pre><code class="language-go"> blocks a Markdown
// renderer writes, which are coloured where they stand. Either way the markup
// carries CSS classes, not colours: the colours are one stylesheet, served at
// /_highlight/style.css with a light and a dark theme, and linked from the head of
// every page that has coloured code.
//
// The pass runs once per render. A cached page is served as it was coloured, so a
// production server colours a static page once, not once per reader.
package highlight

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"strings"
	"testing/fstest"

	"github.com/Elagoht/collage/pkg/collage"
	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Name is the plugin's name, and the key its configuration is found under.
const Name = "elagoht/highlight"

// StylePath is where the stylesheet is served.
const StylePath = "/_highlight/style.css"

// hrefKey is where BeforeRender leaves the stylesheet's URL for AfterRender, in
// the render's values.
var hrefKey = collage.NewKey[func() (string, error)](Name + ":href")

// Options configures the plugin.
type Options struct {
	// Light and Dark are the chroma styles of the two themes, chosen by the
	// reader's prefers-color-scheme: "github", "monokai", "dracula"… Default
	// "github" and "github-dark". Dark "-" leaves the dark theme out, and Light
	// is then used in both.
	Light string `json:"light"`
	Dark  string `json:"dark"`
	// Auto colours the code blocks already on a rendered page:
	// <pre><code class="language-go">, as Markdown renderers write them. On by
	// default; false leaves only {{highlight}}.
	Auto *bool `json:"auto"`
	// NoBackground leaves the block's background colour to the site's own
	// stylesheet, keeping the theme's text colours.
	NoBackground bool `json:"noBackground"`
}

// Plugin colours code.
type Plugin struct {
	opts      Options
	auto      bool
	css       []byte
	style     *chroma.Style
	formatter *chromahtml.Formatter
}

// The hooks the plugin means to implement: a misspelt method would otherwise
// be a hook that silently never fires.
var (
	_ collage.Plugin           = (*Plugin)(nil)
	_ collage.Configurer       = (*Plugin)(nil)
	_ collage.BeforeRenderHook = (*Plugin)(nil)
	_ collage.AfterRenderHook  = (*Plugin)(nil)
)

// New returns a plugin with opts as its starting point, which the application's
// own configuration is then decoded over.
func New(opts Options) *Plugin { return &Plugin{opts: opts} }

func (p *Plugin) Name() string                   { return Name }
func (p *Plugin) Version() string                { return "0.2.5" }
func (p *Plugin) Shutdown(context.Context) error { return nil }

// Configure reads the configuration, makes the stylesheet, and adds
// {{highlight}}. A style chroma does not have stops the application: a
// misspelt theme would otherwise be chroma's fallback, silently.
func (p *Plugin) Configure(_ context.Context, host collage.ConfigHost) error {
	var err error
	if p.opts, err = collage.PluginConfig(host, p.opts); err != nil {
		return err
	}
	if p.opts.Light == "" {
		p.opts.Light = "github"
	}
	if p.opts.Dark == "" {
		p.opts.Dark = "github-dark"
	}
	p.auto = p.opts.Auto == nil || *p.opts.Auto
	light, err := lookup(p.opts.Light)
	if err != nil {
		return err
	}
	p.style = light
	p.formatter = chromahtml.New(chromahtml.WithClasses(true))

	var css bytes.Buffer
	css.WriteString("/* elagoht/highlight: " + p.opts.Light)
	if p.opts.Dark == "-" {
		css.WriteString(" */\n")
		if err := p.writeCSS(&css, light); err != nil {
			return err
		}
	} else {
		dark, err := lookup(p.opts.Dark)
		if err != nil {
			return err
		}
		css.WriteString(", " + p.opts.Dark + " */\n")
		// Each theme under its own media query rather than the dark one over
		// the light: a token the light theme colours and the dark one does not
		// would otherwise keep its light colour on a dark block.
		for _, theme := range []struct {
			scheme string
			style  *chroma.Style
		}{{"light", light}, {"dark", dark}} {
			css.WriteString("@media (prefers-color-scheme: " + theme.scheme + ") {\n")
			if err := p.writeCSS(&css, theme.style); err != nil {
				return err
			}
			css.WriteString("}\n")
		}
	}
	p.css = css.Bytes()

	return host.AddRenderFunc("highlight", func(rc *collage.RenderContext) any { // any: html/template.FuncMap's own value type
		return func(code, lang string) (template.HTML, error) {
			out, err := p.highlight(code, lang, true)
			if err != nil {
				return "", err
			}
			if err := rc.HoistStylesheet(StylePath); err != nil {
				return "", fmt.Errorf("highlight: %w", err)
			}
			return out, nil
		}
	})
}

// lookup finds a chroma style by name.
func lookup(name string) (*chroma.Style, error) {
	style, ok := styles.Registry[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("highlight: chroma has no style %q; see https://xyproto.github.io/splash/docs/", name)
	}
	return style, nil
}

// writeCSS writes style's rules, without the block's background when the site's
// stylesheet owns it. The text colour on the block stays: every token without a
// colour of its own inherits it.
func (p *Plugin) writeCSS(w *bytes.Buffer, style *chroma.Style) error {
	var css bytes.Buffer
	if err := p.formatter.WriteCSS(&css, style); err != nil {
		return fmt.Errorf("highlight: %w", err)
	}
	if !p.opts.NoBackground {
		w.Write(css.Bytes())
		return nil
	}
	for _, line := range strings.SplitAfter(css.String(), "\n") {
		if strings.Contains(line, ".bg {") || strings.Contains(line, ".chroma {") {
			line = dropDeclaration(line, "background-color")
		}
		w.WriteString(line)
	}
	return nil
}

// dropDeclaration removes property's declarations from one line of CSS chroma
// wrote, "selector { property: value; … }".
func dropDeclaration(line, property string) string {
	for {
		i := strings.Index(line, property+":")
		if i < 0 {
			return line
		}
		end := strings.IndexAny(line[i:], ";}")
		if end < 0 {
			return line[:i]
		}
		if line[i+end] == ';' {
			end++
		}
		line = line[:i] + strings.TrimLeft(line[i+end:], " ")
	}
}

// Init serves the stylesheet. It is copied into a static build with the other
// mounted files.
func (p *Plugin) Init(_ context.Context, host collage.Host) error {
	if p.css == nil {
		return errors.New("highlight: register the plugin in Config.Plugins, where Configure runs; {{highlight}} needs it")
	}
	// fstest.MapFS is the standard library's in-memory file system: a mount needs
	// files that seek and a directory a build can walk, and it has both.
	return host.Mount("/_highlight/", fstest.MapFS{"style.css": {Data: p.css}})
}

// highlight colours code as lang. An unknown language is plain text when
// fallback is set, and an error otherwise.
func (p *Plugin) highlight(code, lang string, fallback bool) (template.HTML, error) {
	lexer := lexers.Get(lang)
	if lexer == nil {
		if !fallback {
			return "", errUnknownLanguage
		}
		lexer = lexers.Fallback
	}
	tokens, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return "", fmt.Errorf("highlight: %w", err)
	}
	var out bytes.Buffer
	if err := p.formatter.Format(&out, p.style, tokens); err != nil {
		return "", fmt.Errorf("highlight: %w", err)
	}
	return template.HTML(out.String()), nil // chroma's own output, which escapes the code it wraps
}

var errUnknownLanguage = errors.New("highlight: unknown language")

// OnBeforeRender leaves AfterRender a way to the stylesheet's content-addressed
// URL, which a browser can keep for a year. AfterRender has no render context to
// ask, and before the render starts the mounts are not yet bound to it, so what
// is left is a function asking this render's context once the render has run.
// It lives in the render's values and goes with them.
func (p *Plugin) OnBeforeRender(_ context.Context, ev *collage.BeforeRenderEvent) error {
	if !p.auto || ev.Context == nil {
		return nil
	}
	rc := ev.Context
	hrefKey.Set(rc, func() (string, error) { return rc.Asset(StylePath) })
	return nil
}

// styleKey is the stylesheet's key in the head's hoist area — the one
// RenderContext.HoistStylesheet gives it, so a page whose template called
// {{highlight}} is not given the link twice.
const styleKey = "stylesheet:" + StylePath

// OnAfterRender colours the page's code blocks, and links the stylesheet from
// its head when it did.
//
// The link is hoisted before the blocks are replaced: ev.Hoist finds the place
// the layout put {{hoist "head"}} only in the HTML the render produced, and
// replacing the blocks first would leave it the fallback before </head>.
func (p *Plugin) OnAfterRender(_ context.Context, ev *collage.AfterRenderEvent) error {
	if !p.auto || !bytes.Contains(ev.HTML, []byte("<code")) {
		return nil
	}
	page := ev.HTML
	coloured := p.colour(page)
	if len(coloured) == 0 {
		return nil
	}
	href := StylePath
	if resolve, ok := hrefKey.In(ev.Values); ok && resolve != nil {
		if v, err := resolve(); err == nil {
			href = v
		}
	}
	link := template.HTML(`<link rel="stylesheet" href="` + html.EscapeString(href) + `">`)
	if ev.Hoist("head", styleKey, link) {
		// The head comes before the code, so every block has moved by the
		// link's length. Should one not have — a <pre> written before </head>
		// — the blocks are found again rather than a wrong span replaced.
		shift := len(ev.HTML) - len(page)
		for i, c := range coloured {
			if !bytes.Equal(ev.HTML[c.start+shift:c.end+shift], page[c.start:c.end]) {
				coloured = p.colour(ev.HTML)
				break
			}
			coloured[i].start += shift
			coloured[i].end += shift
		}
	}
	ev.HTML = replace(ev.HTML, coloured)
	return nil
}

// block is one <pre><code class="language-…"> element of a page, by its byte
// offsets.
type block struct {
	start, end int
	lang, code string
}

// coloured is a code block's span of the page and the markup it is replaced
// with.
type coloured struct {
	start, end int
	markup     template.HTML
}

// colour colours every code block of a known language on page, in order. The
// page is tokenised, not parsed: only the blocks' bytes change, and everything
// around them is served as the templates wrote it.
func (p *Plugin) colour(page []byte) []coloured {
	var out []coloured
	for _, b := range codeBlocks(page) {
		markup, err := p.highlight(b.code, b.lang, false)
		if err != nil {
			continue // an unknown language is left as it is
		}
		out = append(out, coloured{start: b.start, end: b.end, markup: markup})
	}
	return out
}

// replace returns page with each block's span replaced by its markup.
func replace(page []byte, blocks []coloured) []byte {
	var out bytes.Buffer
	out.Grow(len(page) + len(page)/2)
	last := 0
	for _, b := range blocks {
		out.Write(page[last:b.start])
		out.WriteString(string(b.markup))
		last = b.end
	}
	out.Write(page[last:])
	return out.Bytes()
}

// codeBlocks finds each <pre> whose only content is one <code> naming a language
// in its class and holding nothing but text. A block with markup inside its code
// has been coloured already, or is something else; either way it is not touched.
func codeBlocks(page []byte) []block {
	const (
		outside = iota
		afterPre
		inCode
		afterCode
	)
	var (
		blocks []block
		state  = outside
		offset int
		cur    block
		code   strings.Builder
	)
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return blocks
		}
		start := offset
		offset += len(z.Raw())
		tok := z.Token()
		if tt == html.StartTagToken && tok.DataAtom == atom.Pre {
			state, cur = afterPre, block{start: start}
			continue
		}
		switch state {
		case afterPre:
			switch {
			case tt == html.TextToken && strings.TrimSpace(tok.Data) == "":
			case tt == html.StartTagToken && tok.DataAtom == atom.Code:
				if cur.lang = language(tok); cur.lang == "" {
					state = outside
					continue
				}
				code.Reset()
				state = inCode
			default:
				state = outside
			}
		case inCode:
			switch {
			case tt == html.TextToken:
				code.WriteString(tok.Data)
			case tt == html.EndTagToken && tok.DataAtom == atom.Code:
				cur.code = code.String()
				state = afterCode
			default:
				state = outside
			}
		case afterCode:
			switch {
			case tt == html.TextToken && strings.TrimSpace(tok.Data) == "":
			case tt == html.EndTagToken && tok.DataAtom == atom.Pre:
				cur.end = offset
				blocks = append(blocks, cur)
				state = outside
			default:
				state = outside
			}
		}
	}
}

// language is the language a <code> element's class names: "language-go", or
// "lang-go" as some renderers write it.
func language(tok html.Token) string {
	for _, a := range tok.Attr {
		if a.Key != "class" {
			continue
		}
		for _, class := range strings.Fields(a.Val) {
			if lang, ok := strings.CutPrefix(class, "language-"); ok && lang != "" {
				return lang
			}
			if lang, ok := strings.CutPrefix(class, "lang-"); ok && lang != "" {
				return lang
			}
		}
	}
	return ""
}
