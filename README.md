# elagoht/highlight

A collage plugin that colours code with [chroma](https://github.com/alecthomas/chroma):
a `{{highlight}}` template function, a pass over every rendered page that colours
the code blocks Markdown renderers write, and a light and dark stylesheet linked
from the pages that have coloured code.

```go
app, err := collage.New(&collage.Config{
	Plugins: []collage.Plugin{highlight.New(highlight.Options{})},
})
```

Requires collage v0.23.0 or later. Register it in `Config.Plugins`: it adds a
template function, which only a plugin registered there can.

Registering it is the whole of it for a site whose pages already hold
`<pre><code class="language-go">` blocks — what
[elagoht/markdown](https://github.com/Elagoht/collage-markdown), goldmark and most
Markdown renderers write. A template colouring code of its own calls the function.

## In a template

```html
{{highlight .Snippet "go"}}
```

renders the code as chroma's `<pre class="chroma"><code>…</code></pre>`, each token
a `<span>` with a class, and links the stylesheet from the page's head. The layout
places it with `{{hoist "head"}}`. A language chroma does not know is shown as plain
text in the same block, so a typo is a grey block rather than a failed page.

## On every page

With `auto` on — the default — every rendered page is read for a `<pre>` holding one
`<code>` whose class names a language, `language-go` or `lang-go`, and nothing but
text. Each is replaced by its coloured markup, where it stands, and the stylesheet
is linked at the end of the page's `<head>`.

The page is tokenised with `golang.org/x/net/html`, not parsed and written back, so
nothing but the blocks changes: the rest of the page is served byte for byte as the
templates wrote it. A block is left exactly as it is when:

- its language is one chroma does not know,
- it names no language,
- its code holds markup — it was coloured already, or is something else,
- it is inside a `<script>`, a `<textarea>` or another element whose text is not
  markup.

A page with no block to colour is not touched, and gets no stylesheet.

### Cost

The pass runs once per render, in the `AfterRender` hook. A cached page is served as
it was coloured — the colouring is in the stored bytes — so a static or incremental
page is coloured once, not once per reader, and a static build colours each page as
it writes it. Only a page that renders on every request colours on every request.

## The stylesheet

Served at `/_highlight/style.css`, and linked by its content-addressed name,
`/_highlight/style.<hash>.css`, which a browser keeps for a year: a changed theme is a
changed name. Each theme sits under its own `prefers-color-scheme` media query, so a
token the light theme colours and the dark one does not never keeps its light colour
on a dark block. A static build writes it beside the pages.

| Option | Default | |
| --- | --- | --- |
| `light` | `github` | the chroma style for a light colour scheme |
| `dark` | `github-dark` | the chroma style for a dark one; `"-"` leaves it out, and `light` is used in both |
| `noBackground` | `false` | leaves the block's background to the site's own stylesheet, keeping the theme's text colours |
| `auto` | `true` | colours the blocks already on rendered pages; `false` leaves only `{{highlight}}` |

The styles are chroma's: `github`, `monokai`, `dracula`, `nord`, `solarized-light`
and [the rest](https://xyproto.github.io/splash/docs/). A name chroma does not have
stops `collage.New`, rather than falling back to one nobody chose.

## Configuration

```json
{
  "elagoht/highlight": {
    "light": "github",
    "dark": "github-dark",
    "noBackground": true,
    "auto": true
  }
}
```

## Limitations

- A site that switches themes with a class or attribute, rather than following the
  reader's system setting, needs its own stylesheet; this one only answers
  `prefers-color-scheme`.
- A fragment served on its own — a fragment path, a pushed update — does not run
  the page hooks, so the `auto` pass does not colour its blocks. `{{highlight}}`
  in the fragment's template does.
- A page with no `</head>` and no `<body>` tag gets its blocks coloured but no
  stylesheet link.
- No line numbers or highlighted lines.
