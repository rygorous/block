package main

import (
	"bytes"
	"fmt"
	"github.com/rygorous/blackfriday"
	"html"
	"html/template"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type DocType int

const (
	DocPost DocType = iota
	DocPage
)

var docType = map[string]DocType{
	"post": DocPost,
	"page": DocPage,
}

type PostID string // Should be unique

type Post struct {
	Id        PostID
	Type      DocType
	Published time.Time
	Updated   time.Time
	Title     string
	Content   template.HTML
	Href      template.URL // permalink
	Kids      []*Post      // for series
	Parent    *Post        // for series

	// Flags for rendering
	Active    bool
	MathJax   bool
	BlockCode bool

	// Internals
	parentId PostID
	markdown []byte // actual markdown code
}

const (
	extensions = blackfriday.EXTENSION_NO_INTRA_EMPHASIS |
		blackfriday.EXTENSION_TABLES |
		blackfriday.EXTENSION_FENCED_CODE |
		blackfriday.EXTENSION_AUTOLINK |
		blackfriday.EXTENSION_SPACE_HEADERS |
		blackfriday.EXTENSION_MATH |
		blackfriday.EXTENSION_LIQUIDTAG
)

func NewPost(filename string, contents []byte) (*Post, error) {
	// attempt to parse ID from file name (if given)
	id := filename
	if idx := strings.LastIndex(filename, "."); idx != -1 {
		id = filename[:idx]
	}

	post := &Post{
		Id: PostID(id),
	}
	err := post.parseContent(contents)
	if err != nil {
		return nil, err
	}

	post.Href = template.URL(post.RenderedName())
	return post, nil
}

var timeFormats = []string{
	"2006-01-02",
	"2006-01-02 15:04",
	"2006-01-02 15:04:05",
}

func parseTime(value string) (time.Time, error) {
	for _, fmt := range timeFormats {
		if time, err := time.Parse(fmt, value); err == nil {
			return time, nil
		}
	}
	return time.Time{}, fmt.Errorf("couldn't parse time %q", value)
}

func (post *Post) parseContent(contents []byte) error {
	rest := contents

	// Lines at the beginning of the file that start with "-" denote property
	// assignments, which are of the form "<key>=<value>".
	for rest[0] == '-' {
		var line string
		var err error

		eol := bytes.IndexByte(rest, '\n')
		if eol != -1 {
			line = string(rest[1:eol])
			rest = rest[eol+1:]
		} else {
			line = string(rest)
			rest = rest[len(rest):]
		}

		// if this line was terminated by CRLF, strip the CR too
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}

		key, value := parseKeyValueLine(line)
		if key == "" {
			return fmt.Errorf("%q: configuration line %q ill-formed", post.Id, line)
		}

		switch key {
		case "title":
			post.Title = value

		case "time":
			if post.Published, err = parseTime(value); err != nil {
				return fmt.Errorf("%q: %s", post.Id, err.Error())
			}

		case "updated":
			if post.Updated, err = parseTime(value); err != nil {
				return fmt.Errorf("%q: %s", post.Id, err.Error())
			}

		case "type":
			var ok bool
			post.Type, ok = docType[value]
			if !ok {
				return fmt.Errorf("%q: unknown type %q", post.Id, value)
			}

		case "parent":
			post.parentId = PostID(value)

		default:
			return fmt.Errorf("%q: unknown property %q", post.Id, key)
		}
	}

	if post.Updated.IsZero() {
		post.Updated = post.Published
	}

	post.markdown = rest

	return post.validate()
}

func (post *Post) validate() error {
	if post.Title == "" {
		return fmt.Errorf("%q: no title set.", post.Id)
	}
	if !post.Standalone() {
		if post.Published.IsZero() {
			return fmt.Errorf("%q: no publication time set", post.Id)
		}
	}
	return nil
}

func parseKeyValueLine(line string) (key string, value string) {
	key = ""
	value = ""
	idx := strings.Index(line, "=")
	if idx != -1 {
		key = line[:idx]
		value = line[idx+1:]
	}
	return
}

// Is this page a standalone page?
func (post *Post) Standalone() bool {
	return post.Type == DocPage
}

// Name of the renderer HTML file for this post
func (post *Post) RenderedName() string {
	return "p" + string(post.Id) + ".html"
}

// Name of the asset path for this post
func (post *Post) AssetPath() string {
	return string(post.Id)
}

func (post *Post) Render(blog *Blog) error {
	renderer := newHtmlRenderer(post, blog)
	post.Content = template.HTML(blackfriday.Markdown(post.markdown, renderer, extensions))
	return renderer.err
}

func parsePostLink(link []byte) PostID {
	if len(link) < 2 || link[0] != '*' {
		return ""
	}

	return PostID(link[1:])
}

func findImage(blog *Blog, post *Post, name string) (uri string, err error, cfg image.Config) {
	// If it's an absolute URL, pass it through - but we don't know the size.
	if url, urlerr := url.Parse(name); urlerr == nil && url.IsAbs() {
		uri = name
		return
	}

	// Else we assume it's a regular path, which has to be relative.
	if path.IsAbs(name) {
		err = fmt.Errorf("%q: image %q needs to be either an absolute URL or a relative path.", post.Id, name)
		return
	}

	// Search first in asset dirs for this post, then parent posts
	for p := post; p != nil; p = p.Parent {
		var file *os.File
		filepath := filepath.Join(blog.PostDir, p.AssetPath(), name)
		if file, err = os.Open(filepath); err == nil {
			cfg, _, err = image.DecodeConfig(file)
			file.Close()

			if err == nil {
				uri = path.Join(p.AssetPath(), name)
				err = blog.AddStaticFile(uri, filepath)
			}
			return
		}
	}

	err = fmt.Errorf("%q: Image %q not found.", post.Id, name)
	return
}

type postHtmlRenderer struct {
	*blackfriday.Html
	post *Post
	blog *Blog
	err  error
}

func newHtmlRenderer(post *Post, blog *Blog) *postHtmlRenderer {
	return &postHtmlRenderer{
		post: post,
		blog: blog,
		Html: blackfriday.HtmlRenderer(
			blackfriday.HTML_USE_SMARTYPANTS|blackfriday.HTML_SMARTYPANTS_LATEX_DASHES,
			"", "").(*blackfriday.Html),
	}
}

func (p *postHtmlRenderer) BlockCode(out *bytes.Buffer, text []byte, lang string) {
	if lang != "" {
		p.post.BlockCode = true
	}
	p.Html.BlockCode(out, text, lang)
}

func (p *postHtmlRenderer) Image(out *bytes.Buffer, link, title, alt []byte) {
	uri, err, cfg := findImage(p.blog, p.post, string(link))
	if err != nil {
		p.err = err
		return
	}

	resized := false
	if cfg.Width > p.blog.MaxImageWidth {
		// Image is wider than maximum, specify smaller size
		// and insert a link to the full-size version
		out.WriteString("<a href=\"")
		out.WriteString(uri)
		out.WriteString("\">")
		if len(title) == 0 {
			title = []byte("Click for full-size version.")
		}

		// Figure out new size (aspect-ratio preserving)
		cfg.Height = int((int64(cfg.Height)*int64(p.blog.MaxImageWidth) + int64(cfg.Width/2)) / int64(cfg.Width))
		cfg.Width = p.blog.MaxImageWidth

		resized = true
	}

	class := []byte(nil)
	if len(alt) > 0 && alt[0] == '{' {
		if end := bytes.IndexByte(alt, '}'); end != -1 {
			class = alt[1:end]
			alt = alt[end+1:]
		}
	}

	out.WriteString("<img src=\"")
	out.WriteString(html.EscapeString(uri))
	out.WriteString("\" alt=\"")
	out.WriteString(html.EscapeString(string(alt)))
	if len(title) > 0 {
		out.WriteString("\" title=\"")
		out.WriteString(html.EscapeString(string(title)))
	}
	out.WriteByte('"')
	if cfg.Width > 0 && cfg.Height > 0 {
		out.WriteString(" width=")
		out.WriteString(strconv.Itoa(cfg.Width))
		out.WriteString(" height=")
		out.WriteString(strconv.Itoa(cfg.Height))
	}
	if len(class) > 0 {
		out.WriteString(" class=\"")
		out.WriteString(html.EscapeString(string(class)))
		out.WriteByte('"')
	}
	out.WriteByte('>')

	if resized {
		out.WriteString("</a>")
	}
}

func (p *postHtmlRenderer) Link(out *bytes.Buffer, link, title, content []byte) {
	if linkTo := parsePostLink(link); linkTo != "" {
		if target := p.blog.FindPostById(linkTo); target != nil {
			link = []byte(target.RenderedName())
			if string(content) == "%" {
				content = []byte(target.Title)
			}
		} else if p.err == nil {
			p.err = fmt.Errorf("%q: contains link to post %q which does not exist.", p.post.Id, linkTo)
		}
	}

	p.Html.Link(out, link, title, content)
}

func (p *postHtmlRenderer) DisplayMath(out *bytes.Buffer, text []byte) {
	p.post.MathJax = true
	out.WriteString("<script type=\"math/tex; mode=display\">")
	out.Write(text)
	out.WriteString("</script><noscript>")
	out.WriteString(html.EscapeString(string(text)))
	out.WriteString("</noscript>")
}

func (p *postHtmlRenderer) InlineMath(out *bytes.Buffer, text []byte) {
	p.post.MathJax = true
	out.WriteString("<script type=\"math/tex\">")
	out.Write(text)
	out.WriteString("</script><noscript>")
	out.WriteString(html.EscapeString(string(text)))
	out.WriteString("</noscript>")
}

func (p *postHtmlRenderer) LiquidTag(out *bytes.Buffer, tag, content []byte) {
}
