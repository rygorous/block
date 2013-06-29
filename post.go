package main

import (
	"bytes"
	"fmt"
	"github.com/russross/blackfriday"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Post struct {
	Filename string
	PageName string // name for standalone posts
	Id       int    // id for regular posts, 0 otherwise
	Time     time.Time
	Title    string
	Content  template.HTML
	Href     template.URL // permalink
	Kids     []*Post      // for series
	Parent   *Post        // for series

	Active bool // used during rendering

	parentId int

	*blackfriday.Html // ugh. but the alternative is implementing all of "Renderer"...
}

func NewPost(filename string, contents []byte) (*Post, error) {
	render := blackfriday.HtmlRenderer(
		blackfriday.HTML_USE_SMARTYPANTS|blackfriday.HTML_SMARTYPANTS_LATEX_DASHES,
		"", "")

	// attempt to parse ID from file name (if given)
	id := 0
	if idx := strings.Index(filename, "-"); idx != -1 {
		val, err := strconv.Atoi(filename[:idx])
		if err != nil {
			return nil, fmt.Errorf("post %q has an ill-formed ID: %q", filename, filename[:idx])
		}
		id = val
	}

	extensions := 0
	extensions |= blackfriday.EXTENSION_NO_INTRA_EMPHASIS
	extensions |= blackfriday.EXTENSION_TABLES
	extensions |= blackfriday.EXTENSION_FENCED_CODE
	extensions |= blackfriday.EXTENSION_AUTOLINK
	extensions |= blackfriday.EXTENSION_SPACE_HEADERS

	post := &Post{
		Filename: filename,
		Id:       id,
		Html:     render.(*blackfriday.Html),
	}

	err := post.parseContent(contents, extensions)
	if err != nil {
		return nil, err
	}

	return post, nil
}

func (post *Post) parseContent(contents []byte, extensions int) error {
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
			post.parentId, err = strconv.Atoi(value)
			if err != nil || post.parentId <= 0 {
				return fmt.Errorf("%q: invalid parent id", post.Filename)
			}

		default:
			return fmt.Errorf("%q: unknown property %q", post.Filename, key)
		}
	}

	post.Content = template.HTML(blackfriday.Markdown(rest, post, extensions))
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

func (p *Post) RenderedName() string {
	if p.Id != 0 {
		return fmt.Sprintf("p%d.html", p.Id)
	} else if p.PageName != "" {
		return fmt.Sprintf("p%s.html", p.PageName)
	}

	return ""
}

type postSortSlice []*Post

func (p postSortSlice) Len() int {
	return len(p)
}

func (p postSortSlice) Less(i, j int) bool {
	return p[i].Id < p[j].Id
}

func (p postSortSlice) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func findPost(posts []*Post, findId int) *Post {
	for _, post := range posts {
		if post.Id == findId {
			return post
		}
	}
	return nil
}

// Perform inter-post linking
func LinkPosts(posts []*Post) error {
	// Sort all posts by ID in increasing order
	sort.Sort(postSortSlice(posts))

	// Determine links between posts.
	for _, post := range posts {
		// Determine permalink
		post.Href = template.URL(post.RenderedName())

		// Link children to their parents (and back)
		if post.parentId != 0 {
			post.Parent = findPost(posts, post.parentId)
			if post.Parent == nil {
				return fmt.Errorf("%q: parent id %d does not correspond to an existing post.", post.Filename, post.parentId)
			} else {
				post.Parent.Kids = append(post.Parent.Kids, post)
			}
		}
	}

	return nil
}

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
