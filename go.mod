// A collage plugin that colours code with chroma: a {{highlight}} template
// function, a pass over every rendered page that colours the code blocks
// Markdown renderers write, and a light and dark stylesheet linked from the pages
// that need it.
module github.com/Elagoht/collage-highlight

go 1.26.0

require (
	github.com/Elagoht/collage v0.23.0
	github.com/alecthomas/chroma/v2 v2.27.0
	golang.org/x/net v0.59.0
)

require github.com/dlclark/regexp2/v2 v2.2.1 // indirect
