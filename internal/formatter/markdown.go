package formatter

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

func markdown(s string) (ConversionResult, error) {
	// Raw HTML is parsed by our HTML converter; nothing is executed or fetched.
	md := goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(gmhtml.WithUnsafe()))
	var b bytes.Buffer
	if err := md.Convert([]byte(s), &b); err != nil {
		return ConversionResult{}, err
	}
	return htmlDocument(b.String())
}
