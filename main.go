package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"code.google.com/p/go.blog/pkg/atom"
)

type Blog struct {
	// Configuration options
	Title          string
	Tagline        string
	Hostname       string
	Url            string
	Author         string
	AtomFeedFile   string
	NumRecentPosts int
	NumFeedPosts   int
	MaxImageWidth  int // if images are wider than this, build a thumbnail.
	PostDir        string
	TemplateDir    string
	OutDir         string

	// Posts
	AllPosts    []*Post // master list of all posts in the blog (includes regular posts and special pages)
	MostRecent  *Post   // most recently added post
	Pages       []*Post // standalone pages
	PostsByDate []*Post // posts sorted by date (this is really only posts, not standalone pages)
	Series      []*Post // list of parent posts for series
	Collections []*Post // list of root posts for collections

	// Files
	files map[string]string // dst_path (relative to output) -> src_path (relative to blog root)

	atomFeed []byte
}

func Warnf(msg string, args ...interface{}) {
	fmt.Fprint(os.Stderr, "Warning: ")
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprint(os.Stderr, "\n")
}

type postsById []*Post

func (p postsById) Len() int           { return len(p) }
func (p postsById) Less(i, j int) bool { return p[i].Id < p[j].Id }
func (p postsById) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

type postsByPublishDate []*Post

func (p postsByPublishDate) Len() int           { return len(p) }
func (p postsByPublishDate) Less(i, j int) bool { return p[i].Published.After(p[j].Published) }
func (p postsByPublishDate) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

type postsByPublishDateAsc []*Post

func (p postsByPublishDateAsc) Len() int           { return len(p) }
func (p postsByPublishDateAsc) Less(i, j int) bool { return p[j].Published.After(p[i].Published) }
func (p postsByPublishDateAsc) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Add initial set of static files
func (blog *Blog) AddStaticFiles() error {
	blog.files = make(map[string]string)

	// Just add all files in "static" dir
	err := filepath.Walk(filepath.Join(blog.TemplateDir, "static"), func(path string, info os.FileInfo, err error) error {
		if relpath, err := filepath.Rel(blog.TemplateDir, path); err == nil && !info.IsDir() {
			blog.files[filepath.ToSlash(relpath)] = path
		}

		return err
	})

	return err
}

// Reads the text files describing all posts from the file system.
func (blog *Blog) ReadPosts() error {
	files, err := filepath.Glob(filepath.Join(blog.PostDir, "*.md"))
	if err != nil {
		return err
	}

	blog.AllPosts = make([]*Post, 0, len(files))
	for _, file := range files {
		text, err := ioutil.ReadFile(file)
		if err != nil {
			return err
		}

		// Generate ID from file name by stripping extension
		id := filepath.Base(file)
		if idx := strings.LastIndex(id, "."); idx != -1 {
			id = id[:idx]
		}

		post, err := NewPost(id, text)
		if err != nil {
			return err
		}

		blog.AllPosts = append(blog.AllPosts, post)
	}
	return nil
}

// Perform inter-post linkage.
func (blog *Blog) LinkPosts() error {
	// Sort all posts by ID in increasing order.
	sort.Sort(postsById(blog.AllPosts))

	// Handle links between posts
	for _, post := range blog.AllPosts {
		// Which index does this end up in?
		if post.Standalone() {
			blog.Pages = append(blog.Pages, post)
		} else {
			// Non-standalone pages get indexed.
			blog.PostsByDate = append(blog.PostsByDate, post)

		}

		// Link children to their parents (and back)
		if post.parentId != "" {
			post.Parent = blog.FindPostById(post.parentId)
			if post.Parent == nil {
				return fmt.Errorf("%q: parent id %q does not correspond to an existing post.", post.Id, post.parentId)
			} else {
				post.Parent.Kids = append(post.Parent.Kids, post)
			}
		}

	}

	// Sort posts by date
	sort.Sort(postsByPublishDate(blog.PostsByDate))

	// Second pass: index series
	for _, post := range blog.PostsByDate {
		// If a post has child posts, it's a series.
		if post.Kids != nil {
			blog.Series = append(blog.Series, post)
		}
	}

	if len(blog.PostsByDate) > 0 {
		blog.MostRecent = blog.PostsByDate[0]
	}

	return nil
}

// Find a post by its ID. This is only guaranteed to work after LinkPosts.
func (blog *Blog) FindPostById(which PostID) *Post {
	for _, post := range blog.AllPosts {
		if post.Id == which {
			return post
		}
	}
	return nil
}

type postInfo struct {
	Root   *Post   // root post for this page
	Docs   []*Post // list of all docs for this page
	Next   *Post
	Prev   *Post
	Blog   *Blog
	Recent []*Post
}

func (blog *Blog) WriteOutput() error {
	// Render all posts' contents
	for _, post := range blog.AllPosts {
		if err := post.Render(blog); err != nil {
			return err
		}
	}
	blog.renderAtomFeed()

	// Wipe existing output dir
	if err := os.RemoveAll(blog.OutDir); err != nil {
		return err
	}

	// Static files
	for dst, src := range blog.files {
		// Make sure the path exists
		outPath := filepath.Join(blog.OutDir, filepath.FromSlash(dst))
		if err := os.MkdirAll(filepath.Dir(outPath), 0733); err != nil {
			return err
		}

		// Copy the file
		if err := copyFile(outPath, src); err != nil {
			return err
		}
	}

	if err := blog.writeOutputPosts(); err != nil {
		return err
	}

	if err := blog.writeAtomFeed(); err != nil {
		return err
	}

	return nil
}

// Writes all posts to the output
func (blog *Blog) writeOutputPosts() error {
	tmpl_text, err := ioutil.ReadFile(filepath.Join(blog.TemplateDir, "template.html"))
	if err != nil {
		return err
	}

	tmpl, err := template.New("post").Parse(string(tmpl_text))
	if err != nil {
		return err
	}

	recent := blog.PostsByDate[:min(len(blog.PostsByDate), blog.NumRecentPosts)]

	// Render pages
	for _, page := range blog.Pages {
		fmt.Printf("processing %q\n", page.Title)
		postinfo := postInfo{
			Root:   page,
			Docs:   []*Post{page},
			Blog:   blog,
			Recent: recent,
		}

		if err = blog.writeOutputPost(&postinfo, tmpl, filepath.Join(blog.OutDir, page.RenderedName())); err != nil {
			return err
		}
	}

	// Render regular posts
	for idx, post := range blog.PostsByDate {
		fmt.Printf("processing %q\n", post.Title)

		postinfo := postInfo{
			Root:   post,
			Docs:   []*Post{post},
			Blog:   blog,
			Recent: recent,
		}
		outname := filepath.Join(blog.OutDir, post.RenderedName())

		if idx > 0 {
			postinfo.Next = blog.PostsByDate[idx-1]
		}

		if idx+1 < len(blog.PostsByDate) {
			postinfo.Prev = blog.PostsByDate[idx+1]
		}

		if err = blog.writeOutputPost(&postinfo, tmpl, outname); err != nil {
			return err
		}

		// If this is the most recent post, make a copy for index.html.
		if post == blog.MostRecent {
			if err = copyFile(filepath.Join(blog.OutDir, "index.html"), outname); err != nil {
				return err
			}
		}
	}

	// Render collections
	for _, root := range blog.Collections {
		fmt.Printf("processing collection %q\n", root.Title)

		postinfo := postInfo{
			Root:   root,
			Docs:   root.Kids,
			Blog:   blog,
			Recent: recent,
		}
		sort.Sort(postsByPublishDateAsc(postinfo.Docs))

		outname := filepath.Join(blog.OutDir, root.RenderedName())
		if err = blog.writeOutputPost(&postinfo, tmpl, outname); err != nil {
			return err
		}
	}

	return nil
}

// Writes a single post to the output
func (blog *Blog) writeOutputPost(info *postInfo, tmpl *template.Template, outname string) error {
	outfile, err := os.Create(outname)
	if err != nil {
		return err
	}

	info.Root.Active = true
	err = tmpl.Execute(outfile, info)
	info.Root.Active = false

	outfile.Close()
	return err
}

func (blog *Blog) writeAtomFeed() error {
	outfile, err := os.Create(filepath.Join(blog.OutDir, blog.AtomFeedFile))
	if err != nil {
		return err
	}

	_, err = outfile.Write(blog.atomFeed)
	outfile.Close()
	return nil
}

func (blog *Blog) renderAtomFeed() error {
	feed := atom.Feed{
		Title: blog.Title,
		ID:    blog.Url + "/block/",
		Link: []atom.Link{
			{
				Rel:  "self",
				Href: blog.Url + "/" + blog.AtomFeedFile,
			},
			{
				Rel:  "alternate",
				Href: blog.Url,
			},
		},
		Author: &atom.Person{
			Name: blog.Author,
		},
	}

	var updated time.Time
	for i, post := range blog.PostsByDate {
		if i >= blog.NumFeedPosts {
			break
		}
		if post.Updated.After(updated) {
			updated = post.Updated
		}
		e := &atom.Entry{
			Title: post.Title,
			ID:    feed.ID + post.AssetPath(),
			Link: []atom.Link{{
				Rel:  "alternate",
				Href: blog.Url + "/" + post.RenderedName(),
			}},
			Published: atom.Time(post.Published),
			Updated:   atom.Time(post.Updated),
			Content: &atom.Text{
				Type: "html",
				Body: string(post.Content),
			},
		}
		feed.Entry = append(feed.Entry, e)
	}
	feed.Updated = atom.Time(updated)
	data, err := xml.Marshal(&feed)
	if err != nil {
		return err
	}
	blog.atomFeed = data
	return nil
}

func copyFile(dstname, srcname string) error {
	srcf, err := os.Open(srcname)
	if err != nil {
		return err
	}
	defer srcf.Close()

	dstf, err := os.Create(dstname)
	if err != nil {
		return err
	}
	defer dstf.Close()

	_, err = io.Copy(dstf, srcf)
	return err
}

// Generates the "Archive" standalone page and adds it to the blog
func (blog *Blog) GenerateArchive() error {
	// Generate archive markdown
	buf := new(bytes.Buffer)
	buf.WriteString("-type=page\n")
	buf.WriteString("-title=Archives\n")

	var prevDate time.Time
	for _, post := range blog.PostsByDate {
		// If the month has changed, print a heading.
		if post.Published.Year() != prevDate.Year() || post.Published.Month() != prevDate.Month() {
			buf.WriteString("\n### ")
			buf.WriteString(post.Published.Format("January 2006"))
			buf.WriteString("\n\n")
		}

		buf.WriteString(fmt.Sprintf("* [%%](*%s)\n", post.Id))
		prevDate = post.Published
	}

	post, err := NewPost("archive", buf.Bytes())
	if err != nil {
		return err
	}

	blog.AllPosts = append(blog.AllPosts, post)
	blog.Pages = append(blog.Pages, post)
	return nil
}

// Generates collections for all series
func (blog *Blog) GenerateCollections() error {
	for _, series := range blog.Series {
		coll, err := NewCollectionPost(series)
		if err != nil {
			return err
		}
		blog.AllPosts = append(blog.AllPosts, coll)
		blog.Collections = append(blog.Collections, coll)
	}
	return nil
}

// Adds a static file to the blog.
func (blog *Blog) AddStaticFile(webpath, srcpath string) error {
	if val, in := blog.files[webpath]; in {
		if val != srcpath {
			return fmt.Errorf("Double definition for path %q - assigned to both %q and %q.", webpath, val, srcpath)
		}
	} else {
		blog.files[webpath] = srcpath
	}
	return nil
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	os.Chdir("c:/Store/Blog")

	// Could (should?) read this from config file.
	blog := &Blog{
		Title:          "The ryg blog",
		Tagline:        "When I grow up I'll be an inventor.",
		Hostname:       "blog.rygorous.org",
		Url:            "http://blog.rygorous.org/test",
		Author:         "Fabian 'ryg' Giesen",
		AtomFeedFile:   "feed.atom.xml",
		NumRecentPosts: 5,
		NumFeedPosts:   10,
		MaxImageWidth:  700,
		PostDir:        "posts",
		TemplateDir:    "template",
		OutDir:         "out",
	}

	check(blog.AddStaticFiles())
	check(blog.ReadPosts())
	check(blog.LinkPosts())
	check(blog.GenerateArchive())
	check(blog.GenerateCollections())
	check(blog.WriteOutput())

	fmt.Println("Done!")
}
