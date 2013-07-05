package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Blog struct {
	// Configuration options
	Url            string
	NumRecentPosts int
	NumFeedPosts   int
	MaxImageWidth  int // if images are wider than this, build a thumbnail.
	PostDir        string
	TemplateDir    string
	OutDir         string

	// Posts
	AllPosts   []*Post // master list of all posts in the blog
	MostRecent *Post   // most recently added post
	Pages      []*Post // standalone pages
	Recent     []*Post // list of recent posts
	Series     []*Post // list of parent posts for series

	// Files
	files map[string]string // dst_path (relative to output) -> src_path (relative to blog root)
}

func Warnf(msg string, args ...interface{}) {
	fmt.Fprint(os.Stderr, "Warning: ")
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprint(os.Stderr, "\n")
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

func reversePosts(posts []*Post) {
	n := len(posts)
	for i := 0; i < n/2; i++ {
		posts[i], posts[n-1-i] = posts[n-1-i], posts[i]
	}
}

func max(a, b int) int {
	if a > b {
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

		post, err := NewPost(filepath.Base(file), text)
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
	sort.Sort(postSortSlice(blog.AllPosts))

	// Handle links between posts
	for _, post := range blog.AllPosts {
		// Determine permalink
		post.Href = template.URL(post.RenderedName())

		if post.PageName != "" {
			// If a post has a page name, it's a standalone page.
			blog.Pages = append(blog.Pages, post)
		} else {
			// Non-standalone pages get indexed.
			blog.Recent = append(blog.Recent, post)

		}

		// Link children to their parents (and back)
		if post.parentId != 0 {
			post.Parent = blog.FindPostById(post.parentId)
			if post.Parent == nil {
				return fmt.Errorf("%q: parent id %d does not correspond to an existing post.", post.Filename, post.parentId)
			} else {
				post.Parent.Kids = append(post.Parent.Kids, post)
			}
		}

	}

	// Second pass: index series
	for _, post := range blog.AllPosts {
		// If a post has child posts, it's a series.
		if post.Kids != nil {
			blog.Series = append(blog.Series, post)
		}
	}

	blog.Recent = blog.Recent[max(len(blog.Recent)-blog.NumRecentPosts, 0):]

	// Reverse both lists so that most recent elements are in front
	reversePosts(blog.Recent)
	reversePosts(blog.Series)

	if len(blog.Recent) > 0 {
		blog.MostRecent = blog.Recent[0]
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
	Post *Post
	Next *Post
	Prev *Post
	Blog *Blog
}

func (blog *Blog) WriteOutput() error {
	// Render all posts' contents
	for _, post := range blog.AllPosts {
		if err := post.Render(blog); err != nil {
			return err
		}
	}

	tmpl_text, err := ioutil.ReadFile(filepath.Join(blog.TemplateDir, "template.html"))
	if err != nil {
		return err
	}

	tmpl, err := template.New("post").Parse(string(tmpl_text))
	if err != nil {
		return err
	}

	// Wipe existing output dir
	if err = os.RemoveAll(blog.OutDir); err != nil {
		return err
	}

	// Static files
	for dst, src := range blog.files {
		// Make sure the path exists
		outPath := filepath.Join(blog.OutDir, filepath.FromSlash(dst))
		if err = os.MkdirAll(filepath.Dir(outPath), 0733); err != nil {
			return err
		}

		// Copy the file
		if err = copyFile(outPath, src); err != nil {
			return err
		}
	}

	// Output posts
	for idx, post := range blog.AllPosts {
		fmt.Printf("processing %d: %q\n", post.Id, post.Title)

		outname := filepath.Join(blog.OutDir, post.RenderedName())
		outfile, err := os.Create(outname)
		if err != nil {
			return err
		}

		postinfo := postInfo{
			Post: post,
			Blog: blog,
		}

		if idx > 0 && blog.AllPosts[idx-1].Id > 0 {
			postinfo.Prev = blog.AllPosts[idx-1]
		}

		if idx+1 < len(blog.AllPosts) && blog.AllPosts[idx+1].Id > 0 {
			postinfo.Next = blog.AllPosts[idx+1]
		}

		post.Active = true
		err = tmpl.Execute(outfile, postinfo)
		post.Active = false

		outfile.Close()
		if err != nil {
			return err
		}

		// If this is the most recent post, copy it to the index post.
		if post == blog.MostRecent {
			err = copyFile(filepath.Join(blog.OutDir, "index.html"), outname)
			if err != nil {
				return err
			}
		}
	}

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

type postDateSortSlice []*Post

func (p postDateSortSlice) Len() int {
	return len(p)
}

func (p postDateSortSlice) Less(i, j int) bool {
	return p[i].Time.After(p[j].Time)
}

func (p postDateSortSlice) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

// Generates the "Archive" standalone page and adds it to the blog
func (blog *Blog) GenerateArchive() error {
	var archivedPosts postDateSortSlice

	// Grab all the ID'ed posts and sort them by date
	for _, post := range blog.AllPosts {
		if post.Id != 0 {
			archivedPosts = append(archivedPosts, post)
		}
	}

	sort.Sort(archivedPosts)

	// Generate archive markdown
	buf := new(bytes.Buffer)
	buf.WriteString("-pagename=archives\n")
	buf.WriteString("# Archives\n")

	var prevDate time.Time
	for _, post := range archivedPosts {
		// If the month has changed, print a heading.
		if post.Time.Year() != prevDate.Year() || post.Time.Month() != prevDate.Month() {
			buf.WriteString("\n### ")
			buf.WriteString(post.Time.Format("January 2006"))
			buf.WriteString("\n\n")
		}

		buf.WriteString(fmt.Sprintf("* [%%](*%d)\n", post.Id))
		prevDate = post.Time
	}

	post, err := NewPost("archive", buf.Bytes())
	if err != nil {
		return err
	}

	blog.AllPosts = append(blog.AllPosts, post)
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
		Url:            "http://blog.rygorous.org",
		NumRecentPosts: 5,
		NumFeedPosts:   10,
		MaxImageWidth:  700,
		PostDir:        "posts",
		TemplateDir:    "template",
		OutDir:         "out",
	}

	check(blog.AddStaticFiles())
	check(blog.ReadPosts())
	check(blog.GenerateArchive())
	check(blog.LinkPosts())
	check(blog.WriteOutput())

	fmt.Println("Done!")
}
