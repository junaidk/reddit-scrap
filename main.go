package main

import (
	"errors"
	"fmt"
	"github.com/anaskhan96/soup"
	"github.com/dustin/go-humanize"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const basePath = "/path/to/download/"
const linkFilePath = "/path/to/reddit_export.html"
const paralledDownload = 4

var dlinks uint64
var exist uint64

var errChan chan errObj

type errObj struct {
	err  error
	url  string
	file string
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	// file obtained with
	// https://redditmanager.com/
	dat, err := ioutil.ReadFile(linkFilePath)

	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	doc := soup.HTMLParse(string(dat))
	links := doc.FindAll("li")

	jobs := make(chan Job, len(links))
	wg := &sync.WaitGroup{}

	for w := 1; w <= paralledDownload; w++ {
		wg.Add(1)
		go worker(w, jobs, wg)
	}

	errChan = make(chan errObj)
	go errRecorder()

	for index, link := range links {
		anchors := link.FindAll("a")
		if len(anchors) < 2 {
			continue
		}
		split := strings.Split(anchors[1].Attrs()["href"], "/")

		name := split[len(split)-2]
		folder := split[4]

		folderPath := path.Join(basePath, folder)
		filePath := path.Join(folderPath, name)
		url := anchors[0].Attrs()["href"]

		job := Job{
			Index:      index,
			Url:        url,
			FilePath:   filePath,
			FolderPath: folderPath,
		}

		jobs <- job

	}

	// to stop the worker, first close the job channel
	close(jobs)

	// then wait using the WaitGroup
	wg.Wait()
	log.Println("===============================")
	log.Println("Total links processed:", dlinks)
	log.Println("Already existing files count:", exist)

	log.Println("===============================")

	close(errChan)
	time.Sleep(1 * time.Second)
}

func errRecorder() {

	var errList []errObj

	for err := range errChan {
		errList = append(errList, err)
	}

	for _, err := range errList {
		log.Println(err.url, "=>", err.err)
	}
}

type Job struct {
	Index      int
	Url        string
	FilePath   string
	FolderPath string
}

func worker(id int, jobs <-chan Job, wg *sync.WaitGroup) {
	defer wg.Done()
	for job := range jobs {
		log.Println("worker", id, "started  ", job.Url)

		newUrl, err := urlGenerator(job.Url)
		if len(newUrl) == 0 {
			errChan <- errObj{
				err: errors.New("error generating url:" + err.Error()),
				url: job.Url,
			}
			continue
		}

		if strings.Contains(newUrl, "redgifs") {
			//time.Sleep(3 * time.Second)
		}
		// counting total link
		atomic.AddUint64(&dlinks, 1)

		//ensureDir(job.FolderPath)
		//putFile(job.FilePath, newUrl)

		//log.Println("worker", id, "finished job", job.Url)
	}
}

func urlGenerator(urlInput string) (string, error) {

	var urlOut string
	var err error

	if strings.Contains(urlInput, "i.redd.it") || strings.Contains(urlInput, "gfycat.com") || strings.Contains(urlInput, "imgur") {

		if strings.Contains(urlInput, "gfycat") {
			urlOut, err = gfyCatMP4Url(urlInput)
			urlOut = strings.Replace(urlOut, "thumbs", "giant", 1)
			urlOut = strings.Replace(urlOut, "-mobile", "", 1)

		} else if strings.Contains(urlInput, "gifv") {
			urlOut = strings.Replace(urlOut, "gifv", "mp4", 1)
		} else {
			urlOut = urlInput
		}
	} else {
		urlOut = ""
	}
	return urlOut, err
}

func gfyCatMP4Url(urlInput string) (string, error) {
	resp, err := soup.Get(urlInput)
	if err != nil {
		log.Println(err.Error())
		return "", err

	}
	doc := soup.HTMLParse(string(resp))
	link := doc.Find("meta", "property", "og:video")

	if link.Error != nil {
		return "", err
	}

	urlOut, ok := link.Attrs()["content"]
	if !ok {
		return "", err
	}
	return urlOut, nil
}

func putFile(fileName, url string) {

	client := httpClient()
	resp, err := client.Get(url)
	if err != nil {
		errChan <- errObj{
			err: err,
			url: url,
		}
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode > 299 {
		errChan <- errObj{
			err: errors.New(fmt.Sprintf("url %s returned status code %d", url, resp.StatusCode)),
			url: url,
		}
		return
	}
	ext := getExtention(resp.Header["Content-Type"][0])
	filePath := fileName + "." + ext

	//fmt.Println("filePath:",filePath)
	if ensureFile(filePath) {
		// counting already existed files
		atomic.AddUint64(&exist, 1)
		return
	}
	file := createImage(filePath)

	// show progress bar
	//counter := &WriteCounter{}
	//size, err := io.Copy(file, io.TeeReader(resp.Body, counter))
	size, err := io.Copy(file, resp.Body)

	defer file.Close()
	if err != nil {
		//log.Println(err)
		errChan <- errObj{
			err: err,
			url: url,
		}

		os.Remove(filePath)
	} else {
		log.Printf("Just Downloaded a file %s with size %s\n and link %s", fileName, humanize.Bytes(uint64(size)), url)
	}
}

func getExtention(url string) string {

	splits := strings.Split(url, "/")
	ext := splits[len(splits)-1]

	return ext
}

func httpClient() *http.Client {
	client := http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			r.URL.Opaque = r.URL.Path
			return nil
		},
	}

	return &client
}

func createImage(fileName string) *os.File {
	file, err := os.Create(fileName)

	if err != nil {
		log.Println(err)
	}
	return file
}

func ensureFile(filePath string) bool {

	if _, err := os.Stat(filePath); err == nil {
		return true
	} else if os.IsNotExist(err) {
		return false
	}
	return false
}

func ensureDir(dirName string) error {
	err := os.Mkdir(dirName, 755)
	if err == nil || os.IsExist(err) {
		return nil
	} else {
		return err
	}
}

type WriteCounter struct {
	Total uint64
}

func (wc *WriteCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Total += uint64(n)
	wc.PrintProgress()
	return n, nil
}

// PrintProgress prints the progress of a file write
func (wc WriteCounter) PrintProgress() {
	// Clear the line by using a character return to go back to the start and remove
	// the remaining characters by filling it with spaces
	fmt.Printf("\r%s", strings.Repeat(" ", 50))

	// Return again and print current status of download
	// We use the humanize package to print the bytes in a meaningful way (e.g. 10 MB)
	fmt.Printf("\rDownloading... %s complete", humanize.Bytes(wc.Total))
}
