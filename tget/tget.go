package tget

import (
	_ "embed"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/vbauerster/mpb/v8"
)

//go:embed torrc
var TorrcTemplate string

type TorGet struct {
}

var Version = "v0.3.2"

func PrepareRequest(req *http.Request, headers []string, cookies, useragent, body string) {
	for _, h := range headers {
		split := strings.SplitN(h, ":", 2)
		k := split[0]
		v := strings.TrimSpace(split[1])
		req.Header.Add(k, v)
	}
	if cookies != "" {
		req.Header.Add("Cookie", cookies)
	}
	if len(body) > 0 {
		req.Body = io.NopCloser(strings.NewReader(body))
	}

	if useragent != "" {
		req.Header.Set("User-Agent", useragent)
	}
}

func PartialDownloadUrl(c *http.Client, req *http.Request, outPath string, chunkStart, chunkEnd int64, followRedir, tryContinue, overwrite bool, bar *mpb.Bar) {
	var currentSize int64 = 0
	currentSize = chunkStart
	if tryContinue {
		// Entering here means that the file exists and has a size > 0
		stat, _ := os.Stat(outPath)
		currentSize += stat.Size()
		//log.Printf("%v found on disk, user asked to attempt a resume (%d)\n", outPath, currentSize)
	}
	if overwrite {
		currentSize = chunkStart
	}
	req.Header.Set("range", fmt.Sprintf("bytes=%d-%d", currentSize, chunkEnd))

	resp, err := c.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer resp.Body.Close()
	//log.Printf("client downloading %v to %v (%d) %v (size: %v)\n", req.URL.String(), outPath, resp.StatusCode, resp.Header.Get("Location"), resp.Header.Get("content-length"))

	if (resp.StatusCode >= 300 && resp.StatusCode <= 399) || resp.Header.Get("location") != "" {
		if followRedir {
			redirectUrl, err := resp.Location()
			if err != nil {
				bar.Abort(false)
				return
			}

			// create a new GET request to follow the redirect
			req.URL = redirectUrl
			resp, err = c.Do(req)
			if err != nil {
				bar.Abort(false)
				return
			}
			defer resp.Body.Close()
		} else {
			//log.Println("aborting")
			bar.Abort(false)
			return
		}
	}

	out, err := os.OpenFile(outPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		log.Println(err)
		return
	}
	defer out.Close()
	out.Seek(int64(currentSize), 0)

	totalBytes, err := strconv.Atoi(resp.Header.Get("content-length"))
	if err != nil {
		log.Println(req.URL, "couldn't read content-lenght")
		totalBytes = -1
	}

	//log.Printf("%v size is %d\n", req.URL, totalBytes)
	bar.SetTotal(int64(totalBytes), false)

	pw := bar.ProxyWriter(out)
	defer pw.Close()
	pr := bar.ProxyReader(resp.Body)
	defer pr.Close()

	_, err = io.Copy(pw, pr)
	if err != nil && err != io.EOF {
		//_, err := io.ReadAll(pr)
		//log.Println("body:", body)
		log.Println("copy error:", err)
		bar.Abort(false)
	}
	bar.SetTotal(-1, true) // set as complete
	//log.Println(out.Name(), "done")
}

func DownloadUrl(c *http.Client, req *http.Request, outPath string, followRedir, tryContinue, overwrite bool, bar *mpb.Bar) {
	currentSize := 0
	if stat, err := os.Stat(outPath); err == nil {
		// TODO: implement proper resume with etag/filehash/Ifrange etc, check if accpet-range is supported, etc
		if tryContinue {
			currentSize := stat.Size()
			//log.Printf("%v found on disk, user asked to attempt a resume (%d)\n", outPath, currentSize)
			req.Header.Set("range", fmt.Sprintf("bytes=%d-", currentSize))
		}

		//in case overwrite is set, start from the beginning anyway
		if overwrite {
			currentSize = 0
			req.Header.Del("range")
		}
	}

	resp, err := c.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer resp.Body.Close()
	//log.Printf("client downloading %v to %v (%d) %v (size: %v)\n", req.URL.String(), outPath, resp.StatusCode, resp.Header.Get("Location"), resp.Header.Get("content-length"))

	if (resp.StatusCode >= 300 && resp.StatusCode <= 399) || resp.Header.Get("location") != "" {
		if followRedir {
			redirectUrl, err := resp.Location()
			if err != nil {
				bar.Abort(false)
				return
			}

			// create a new GET request to follow the redirect
			req.URL = redirectUrl
			resp, err = c.Do(req)
			if err != nil {
				bar.Abort(false)
				return
			}
			defer resp.Body.Close()
		} else {
			//log.Println("aborting")
			bar.Abort(false)
			return
		}
	}

	out, err := os.OpenFile(outPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		log.Println(err)
		return
	}
	defer out.Close()
	out.Seek(int64(currentSize), 0)

	totalBytes, err := strconv.Atoi(resp.Header.Get("content-length"))
	if err != nil {
		log.Println(req.URL, "couldn't read content-lenght")
		totalBytes = -1
	}

	//log.Printf("%v size is %d\n", req.URL, totalBytes)
	bar.SetTotal(int64(totalBytes), false)

	pw := bar.ProxyWriter(out)
	pr := bar.ProxyReader(resp.Body)
	defer pr.Close()
	defer pw.Close()

	_, err = io.Copy(pw, pr)
	if err != nil && err != io.EOF {
		//_, err := io.ReadAll(pr)
		//log.Println("body:", body)
		log.Println("copy error:", err)
		bar.Abort(false)
	}

	bar.SetTotal(-1, true) // set as complete
	//log.Println(out.Name(), "done")
}

func MergeChunkFiles(chunkFiles []string, finalPath string) error {
	out, err := os.OpenFile(finalPath, os.O_CREATE | os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open final file %s: %w", finalPath, err)
	}
	defer out.Close()

	for i, chunk := range chunkFiles {
		f, err := os.Open(chunk)
		if err != nil {
			return fmt.Errorf("failed to open chunk %s: %w", chunk, err)
		}
		defer f.Close()

		_, err = io.Copy(out, f)
		if err != nil {
			return fmt.Errorf("merge error on chunk #%d: %w", i, err)
		}
	}
	return nil
}

func GetFilename(file string) string {
	attempt := 0
	currentFile := file
	for {
		_, err := os.Stat(currentFile)
		if os.IsNotExist(err) {
			return currentFile
		}
		attempt++
		currentFile = fmt.Sprintf("%s.%d", file, attempt)
	}
}

func GetFreePorts(n int) (ports []int, err []error) {
	for i := 0; i < n; i++ {
		if a, e := net.ResolveTCPAddr("tcp", "localhost:0"); e == nil {
			var l *net.TCPListener
			if l, e = net.ListenTCP("tcp", a); e == nil {
				defer l.Close()
				ports = append(ports, l.Addr().(*net.TCPAddr).Port)
			} else {
				err = append(err, e)
			}
		} else {
			err = append(err, e)
		}
	}

	return ports, err
}

func HandleRedirect(c *http.Client, req *http.Request, resp *http.Response, err error, followRedir bool) *http.Response {
	// Handle redirects
	if (resp.StatusCode >= 300 && resp.StatusCode <= 399) || resp.Header.Get("location") != "" {
		if followRedir {
			redirectUrl, err := resp.Location()
			if err != nil {
				panic(err)
			}

			// create a new GET request to follow the redirect
			req.URL = redirectUrl
			resp, err = c.Do(req)
			if err != nil {
				panic(err)
			}
			defer resp.Body.Close()
		} else {
			//log.Println("aborting")
			panic(err)
		}
	}
	return resp
}


func SplitAndDistributeDownloads(clients []*http.Client, urls []string, instances int, headers []string, cookies, useragent, body string, followRedir bool) ([][][]int64, [][]string) {
	// Temporary lists to categorize URLs based on Range support
	supportedUrls := make(map[string]int64)
	var unsupportedUrls []string

	for i, url := range urls {
		// Request full file
		req, err := http.NewRequest("HEAD", url, nil)
		if err != nil {
			panic(fmt.Errorf("failed to create HEAD request for %s: %v", url, err))
		}
		PrepareRequest(req, headers, cookies, useragent, body)

		resp, err := clients[i].Do(req)
		if err != nil {
			unsupportedUrls = append(unsupportedUrls, url)
			continue
		}
		defer resp.Body.Close()

		resp = HandleRedirect(clients[i], req, resp, err, followRedir)

		cl := resp.Header.Get("content-length")
		if cl == "" {
			unsupportedUrls = append(unsupportedUrls, url)
			continue
		}
		totalSize, err := strconv.ParseInt(cl, 10, 64)
		if err != nil {
			unsupportedUrls = append(unsupportedUrls, url)
			continue
		}

		// Request a fraction of the file to check if Range header is supported
		tenPercent := int64(totalSize * 10 / 100)
		req, err = http.NewRequest("HEAD", url, nil)
		if err != nil {
			panic(fmt.Errorf("failed to create ranged HEAD request for %s: %v", url, err))
		}
		PrepareRequest(req, headers, cookies, useragent, body)
		req.Header.Set("range", fmt.Sprintf("bytes=%d-%d", 0, tenPercent))

		respr, err := clients[i].Do(req)
		if err != nil {
			unsupportedUrls = append(unsupportedUrls, url)
			continue
		}
		defer respr.Body.Close()

		respr = HandleRedirect(clients[i], req, respr, err, followRedir)

		clr := respr.Header.Get("content-length")
		if clr == "" {
			unsupportedUrls = append(unsupportedUrls, url)
			continue
		}
		partialSize, err := strconv.ParseInt(clr, 10, 64)
		if err != nil {
			unsupportedUrls = append(unsupportedUrls, url)
			continue
		}

		if partialSize == tenPercent+1 {
			supportedUrls[url] = totalSize
		} else {
			unsupportedUrls = append(unsupportedUrls, url)
		}
	}

	var chunkedSupported [][][]int64
	var chunkedUnsupported [][]string

	// Split URL in instances and distribute them
	if len(supportedUrls) > 0 {
		sizes := make([]int64, 0, len(supportedUrls))
		for _, s := range supportedUrls {
			sizes = append(sizes, s)
		}
		chunkedSupported = ChunkRanges(sizes, int64(instances))
	}

	// Distribute URLs into instances
	if len(unsupportedUrls) > 0 {
		chunkedUnsupported = ChunkBy(unsupportedUrls, instances)
	}

	return chunkedSupported, chunkedUnsupported
}

func ChunkRanges(m []int64, n int64) [][][]int64 {
	if n <= 0 {
		return [][][]int64{}
	}

	result := make([][][]int64, n)
	var i int64
	for i = 0; i < n; i++ {
		part := make([][]int64, len(m))
		for j, mm := range m {
			step := mm / n
			lower := i * step

			upper := mm
			if i < n-1 {
				upper = lower + step - 1
			}
			part[j] = []int64{lower, upper}
		}
		result[i] = part
	}
	return result
}

func ChunkBy[T any](a []T, n int) [][]T {
	if n <= 0 {
		return [][]T{}
	}

	batches := make([][]T, 0, n)

	var size, lower, upper int
	l := len(a)

	for i := 0; i < n; i++ {
		lower = i * l / n
		upper = ((i + 1) * l) / n
		size = upper - lower

		a, batches = a[size:], append(batches, a[0:size:size])
	}
	return batches
}
