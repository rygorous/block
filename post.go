package main

import (
	"bytes"
	"fmt"
	"github.com/disintegration/imaging"
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

type PostID int // 0 if not an indexed post.

type Post struct {
	Filename string
	PageName string // name for standalone posts
	Id       PostID // id for regular posts, 0 otherwise
	Time     time.Time
	Title    string
	Content  template.HTML
	Href     template.URL // permalink
	Kids     []*Post      // for series
	Parent   *Post        // for series

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
		blackfriday.EXTENSION_MATH
)

func NewPost(filename string, contents []byte) (*Post, error) {
	// attempt to parse ID from file name (if given)
	id := 0
	if idx := strings.Index(filename, "-"); idx != -1 {
		val, err := strconv.Atoi(filename[:idx])
		if err != nil {
			return nil, fmt.Errorf("post %q has an ill-formed ID: %q", filename, filename[:idx])
		}
		id = val
	}

	post := &Post{
		Filename: filename,
		Id:       PostID(id),
	}
	err := post.parseContent(contents)
	if err != nil {
		return nil, err
	}

	blackfriday.Markdown(post.markdown, newAnalyzer(post), extensions)
	return post, nil
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
			return fmt.Errorf("%q: configuration line %q ill-formed", post.Filename, line)
		}

		switch key {
		case "time":
			post.Time, err = time.Parse("2006-01-02", value)
			if err != nil {
				return fmt.Errorf("%q: error while trying to parse time: %q", post.Filename, err.Error())
			}

		case "pagename":
			post.PageName = value

		case "parent":
			parentId, err := strconv.Atoi(value)
			if err != nil || parentId <= 0 {
				return fmt.Errorf("%q: invalid parent id", post.Filename)
			} else {
				post.parentId = PostID(parentId)
			}

		default:
			return fmt.Errorf("%q: unknown property %q", post.Filename, key)
		}
	}
	post.markdown = rest

	return post.validate()
}

func (post *Post) validate() error {
	if post.Id != 0 {
		if post.Time.IsZero() {
			return fmt.Errorf("post %q doesn't have a time set", post.Filename)
		}
	} else {
		if post.PageName == "" {
			return fmt.Errorf("post %q has neither an ID nor a page name", post.Filename)
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

// Name of the renderer HTML file for this post
func (post *Post) RenderedName() string {
	if post.Id != 0 {
		return fmt.Sprintf("p%d.html", post.Id)
	} else if post.PageName != "" {
		return fmt.Sprintf("p%s.html", post.PageName)
	}

	return ""
}

// Name of the asset path for this post
func (post *Post) AssetPath() string {
	if post.Id != 0 {
		return strconv.Itoa(int(post.Id))
	} else {
		return post.PageName
	}

	return ""
}

func (post *Post) Render(blog *Blog) error {
	renderer := newHtmlRenderer(post, blog)
	post.Content = template.HTML(blackfriday.Markdown(post.markdown, renderer, extensions))
	return renderer.err
}

func parsePostLink(link []byte) PostID {
	if len(link) < 2 || link[0] != '*' {
		return 0
	}

	if value, err := strconv.Atoi(string(link[1:])); err == nil {
		return PostID(value)
	}

	return 0
}

func findImage(blog *Blog, post *Post, name string) (uri string, err error, cfg image.Config) {
	// If it's an absolute URL, pass it through - but we don't know the size.
	if url, urlerr := url.Parse(name); urlerr == nil && url.IsAbs() {
		uri = name
		return
	}

	// Else we assume it's a regular path, which has to be relative.
	if path.IsAbs(name) {
		err = fmt.Errorf("%q: image %q needs to be either an absolute URL or a relative path.", post.Filename, name)
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

	err = fmt.Errorf("%q: Image %q not found.", post.Filename, name)
	return
}

func makeThumbnail(blog *Blog, in_uri string) (uri string, err error, cfg image.Config) {
	var file *os.File
	fspath := blog.files[in_uri]
	if file, err = os.Open(fspath); err == nil {
		var src image.Image
		if src, _, err = image.Decode(file); err == nil {
			dst := imaging.Resize(src, blog.MaxImageWidth, 0, imaging.MitchellNetravali)
			extra := ".thumb.jpg"
			if !dst.Opaque() {
				extra = ".thumb.png"
			}

			fsthumb := fspath + extra
			uri = in_uri + extra
			imaging.Save(dst, fsthumb)
			blog.AddStaticFile(uri, fsthumb)

			cfg.Width = dst.Bounds().Dx()
			cfg.Height = dst.Bounds().Dy()
		}
	}

	return
}

type postAnalyzer struct {
	*blackfriday.Null
	post *Post
}

func newAnalyzer(post *Post) blackfriday.Renderer {
	return &postAnalyzer{Null: &blackfriday.Null{}, post: post}
}

func (p *postAnalyzer) BlockCode(out *bytes.Buffer, text []byte, lang string) {
	if lang != "" {
		p.post.BlockCode = true
	}
}

func (p *postAnalyzer) Header(out *bytes.Buffer, text func() bool, level int) {
	if level == 1 {
		if p.post.Title != "" {
			Warnf("Post %q defines multiple titles! (Level-1 headlines)", p.post.Filename)
		}

		out.Truncate(0)
		text()
		p.post.Title = string(out.Bytes())
		out.Truncate(0)
	}
}

func (p *postAnalyzer) DisplayMath(out *bytes.Buffer, text []byte) {
	p.post.MathJax = true
}

func (p *postAnalyzer) InlineMath(out *bytes.Buffer, text []byte) {
	p.post.MathJax = true
}

func (p *postAnalyzer) NormalText(out *bytes.Buffer, text []byte) {
	out.Write(text)
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

func (p *postHtmlRenderer) Header(out *bytes.Buffer, text func() bool, level int) {
	if level != 1 {
		p.Html.Header(out, text, level)
	}
}

func (p *postHtmlRenderer) Image(out *bytes.Buffer, link, title, alt []byte) {
	uri, err, cfg := findImage(p.blog, p.post, string(link))
	if err != nil {
		p.err = err
		return
	}

	resized := false
	if cfg.Width > p.blog.MaxImageWidth {
		Warnf("image %q is wider (%d pixels) than maximum of %d pixels.", uri, cfg.Width, p.blog.MaxImageWidth)

		out.WriteString("<a href=\"")
		out.WriteString(uri)
		out.WriteString("\">")
		if len(title) == 0 {
			title = []byte("Click for full-size version.")
		}

		if uri, err, cfg = makeThumbnail(p.blog, uri); err != nil {
			p.err = err
			return
		}

		resized = true
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
	out.WriteByte('>')

	if resized {
		out.WriteString("</a>")
	}
}

func (p *postHtmlRenderer) Link(out *bytes.Buffer, link, title, content []byte) {
	if linkTo := parsePostLink(link); linkTo != 0 {
		if target := p.blog.FindPostById(linkTo); target != nil {
			link = []byte(target.RenderedName())
			title = []byte(target.Title)
			if string(content) == "%" {
				content = title
			}
		} else if p.err == nil {
			p.err = fmt.Errorf("%q: contains link to post %d which does not exist.", p.post.Filename, linkTo)
		}
	}

	p.Html.Link(out, link, title, content)
}

func (p *postHtmlRenderer) DisplayMath(out *bytes.Buffer, text []byte) {
	out.WriteString("\\[")
	p.Html.DisplayMath(out, text)
	out.WriteString("\\]")
}

func (p *postHtmlRenderer) InlineMath(out *bytes.Buffer, text []byte) {
	out.WriteString("\\(")
	p.Html.InlineMath(out, text)
	out.WriteString("\\)")
}
