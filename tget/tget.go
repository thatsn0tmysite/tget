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

func PrepareRequest(req *http.Request, headers []string, cookies string, body string) {
	for _, h := range headers {
		split := strings.Split(h, "=")
		k := split[0]
		v := ""
		if len(split) > 1 {
			v = strings.Join(split[1:], "=")
		}

		req.Header.Add(k, v)
	}
	if cookies != "" {
		req.Header.Add("Cookie", cookies)
	}
	if len(body) > 0 {
		req.Body = io.NopCloser(strings.NewReader(body))
	}
}

func DownloadUrl(c *http.Client, req *http.Request, outPath string, tryContinue, overwrite bool, bar *mpb.Bar) {
	log.Printf("client downloading %v to %v\n", req.URL.String(), outPath)

	currentSize := 0
	if stat, err := os.Stat(outPath); err == nil {
		// TODO: implement proper resume with etag/filehash/Ifrange etc, check if accpet-range is supported, etc
		if tryContinue && !overwrite {
			currentSize := stat.Size()
			log.Printf("%v found on disk, user asked to attempt a resume (%d)\n", outPath, currentSize)
			req.Header.Set("range", fmt.Sprintf("%d-", currentSize))
		}

		//in case overwrite is set, start from the beginning anyway
		if overwrite {
			currentSize = 0
			req.Header.Del("range")
		}
	}

	out, err := os.OpenFile(outPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		log.Println(err)
		return
	}
	defer out.Close()
	out.Seek(0, currentSize)

	resp, err := c.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer resp.Body.Close()

	totalBytes, err := strconv.Atoi(resp.Header.Get("content-length"))
	if err != nil {
		totalBytes = -1
	}

	log.Printf("%v size is %d\n", req.URL, totalBytes)
	bar.SetTotal(int64(totalBytes), false)

	for {
		var buf []byte

		_, err := resp.Body.Read(buf)
		if err != nil {
			log.Println(err)
			break
		}
		if err == io.EOF {
			break
		}

		written, err := out.Write(buf)
		if err != nil {
			log.Println(err)
			break
		}

		//bar.IncrInt64(int64(written))
		bar.IncrBy(written)

		if written == 0 {
			break
		}
	}
}

func GetFilename(file string, attempt int) string {
	if _, err := os.Stat(file); err == os.ErrNotExist {
		return file // valid filename, return
	} else if err == nil {
		return GetFilename(fmt.Sprintf("%s.%d", file, attempt+1), attempt+1)
	}

	return file
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
