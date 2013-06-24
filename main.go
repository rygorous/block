package main

import (
	"fmt"
	"html/template"
        "io"
	"io/ioutil"
	"os"
	"path/filepath"
)

const (
        baseUrl string = "http://blog.rygorous.org"
        numRecentPosts = 5
)

func Warnf(msg string, args ...interface{}) {
	fmt.Fprint(os.Stderr, "Warning: ")
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprint(os.Stderr, "\n")
}

func readPosts() ([]*Post, error) {
	files, err := filepath.Glob("posts/*.md")
	if err != nil {
		return nil, err
	}

	posts := make([]*Post, 0, len(files))
	for _, file := range files {
		text, err := ioutil.ReadFile(file)
		if err != nil {
			return nil, err
		}

                post, err := NewPost(filepath.Base(file), text)
                if err != nil {
                        return nil, err
                }

		posts = append(posts, post)
	}
	return posts, nil
}

type postInfo struct {
	Post   *Post
        MostRecent *Post
        Pages []*Post // list of standalone pages
	Recent []*Post // list of recent posts
	Series []*Post // list of parent posts for series
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

func processPosts(posts []*Post) error {
	tmpl_text, err := ioutil.ReadFile("template/template.html")
	if err != nil {
		return err
	}

	tmpl, err := template.New("post").Parse(string(tmpl_text))
	if err != nil {
		return err
	}

        err = LinkPosts(posts)
        if err != nil {
                return err
        }

        // Determine list of "most recent" posts and "series" posts
        postinfo := new(postInfo)
        for _, post := range posts {
                if post.PageName != "" {
                        // If a post has a page name, it's a standalone page.
                        postinfo.Pages = append(postinfo.Pages, post)
                } else {
                        // Non-standalone pages get indexed.
                        postinfo.Recent = append(postinfo.Recent, post)

                        // If a post has child posts, it's a series.
                        if post.Kids != nil {
                                postinfo.Series = append(postinfo.Series, post)
                        }
                }
        }

        postinfo.Recent = postinfo.Recent[max(len(posts) - numRecentPosts, 0):]

        // Reverse both lists so that most recent elements are in front
        reversePosts(postinfo.Recent)
        reversePosts(postinfo.Series)

        postinfo.MostRecent = postinfo.Recent[0]

        // Output files
	for _, post := range posts {
                fmt.Printf("processing %d: %q\n", post.Id, post.Title)

                outfile, err := os.Create("out/" + post.RenderedName())
                if err != nil {
                        return err
                }
                defer outfile.Close()

                post.Active = true

                postinfo.Post = post
                if err = tmpl.Execute(outfile, postinfo); err != nil {
                        return err
                }

                post.Active = false
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

func prepareOutput() error {
        // don't check for (expected) error here (directory already exists)
        os.Mkdir("out", 0733)

        var files []string = []string{
                "style.less",
                "less.js",
        }

        for _, file := range files {
                err := copyFile("out/" + file, "template/" + file)
                if err != nil {
                        return err
                }
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
	posts, err := readPosts()
        check(err)
        check(prepareOutput())
        check(processPosts(posts))

	fmt.Println("Done!")
}
