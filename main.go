package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
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

		posts = append(posts, NewPost(file, text))
	}
	return posts, nil
}

type postInfo struct {
	post   *Post
	recent []*Post // list of recent posts
	series []*Post // list of parent posts for series
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

	for _, post := range posts {
		buf := new(bytes.Buffer)
                tmpl.Execute(buf, &postInfo{
                        post: post,
                })
	}

	return nil
}

func prepareOutput() {
	os.Mkdir("out", 0733)
}

func main() {
	os.Chdir("c:/Store/Blog")
	posts, err := readPosts()
	if err != nil {
		panic(err)
	}
	prepareOutput()
	processPosts(posts)

	fmt.Println("Done!")
}
