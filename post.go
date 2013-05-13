package main

import (
	"bytes"
	"github.com/russross/blackfriday"
	"html/template"
)

type Post struct {
	Filename string
	Id       int
	Time     string
	Title    string
	Content  template.HTML
	Href     template.URL // permalink
	Kids     []*Post      // for series

        *blackfriday.Html // ugh. but the alternative is implementing all of "Renderer"...
}

func NewPost(filename string, contents []byte) *Post {
	render := blackfriday.HtmlRenderer(
		blackfriday.HTML_USE_SMARTYPANTS|blackfriday.HTML_SMARTYPANTS_LATEX_DASHES,
		"", "")

	// TODO parse ID from file name!

	extensions := 0
	extensions |= blackfriday.EXTENSION_NO_INTRA_EMPHASIS
	extensions |= blackfriday.EXTENSION_TABLES
	extensions |= blackfriday.EXTENSION_FENCED_CODE
	extensions |= blackfriday.EXTENSION_AUTOLINK
	extensions |= blackfriday.EXTENSION_SPACE_HEADERS

	post := &Post{
		Filename: filename,
                Id: 1,
		Html: render.(*blackfriday.Html),
	}

	post.Content = template.HTML(blackfriday.Markdown(contents, post, extensions))

	return post
}

// ---- Bunch of functions here to implement the Renderer interface

func (p *Post) Header(out *bytes.Buffer, text func() bool, level int) {
	if level != 1 {
		p.Html.Header(out, text, level)
		return
	}

	if p.Title != "" {
		Warnf("Post %q defines multiple titles! (Level-1 headlines)", p.Filename)
	}

	marker := out.Len()
	text()
	p.Title = string(out.Bytes()[marker:])
	out.Truncate(marker)
}

