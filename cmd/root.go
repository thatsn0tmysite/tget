/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"crypto/tls"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
	"torget/tget"

	"github.com/cretz/bine/tor"
	"github.com/google/uuid"
	"github.com/hairyhenderson/go-which"
	"github.com/panjf2000/ants"
	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/net/proxy"
)

type tgetFlags struct {
	socksVersion string
	conf         string
	ports        []uint
	fromFile     bool
	concurrency  int
	instances    int
	getTor       bool
	torPath      string
	logPath      string
	outPath      string
	host         string
	verbose      bool
	maxWait      int
	ovewrite     bool
	tryContinue  bool
	testDomain   string

	body      string
	method    string
	cookies   string
	headers   []string
	unsafeTLS bool
}

var flags tgetFlags

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "tget [flags] <url|file> [...url|file]",
	Short: "A (fast) Tor file downloader",
	Long: `Torget is a Tor aware file downloader which uses multiple Tor instances to try to use all available bandwidth.
	Made by thatsn0tmysite (aka n0tme) Blog: https://thatsn0tmy.site`,
	Run: func(cmd *cobra.Command, args []string) {
		//Setup logger
		if flags.logPath != "" {
			logFile, err := os.Create(flags.logPath)
			if err != nil {
				log.Fatalf("error creating logfile: %s\n", err)
			}
			mw := io.MultiWriter(os.Stdout, logFile)
			log.SetOutput(mw)
		}

		if len(args) < 1 {
			fmt.Println("No urls or files specified!")
			cmd.Root().Help()
			return
		}

		//Check if tor is installed (aka we have a valid tor-path)
		_, err := os.Stat(flags.torPath)
		if flags.torPath == "" || errors.Is(err, os.ErrNotExist) {
			log.Fatalf("path does not exist: %s\n", flags.torPath)
			if !flags.getTor {
				return
			}
		}

		if flags.instances < 1 {
			log.Println("at least one instance is needed, instances set to 1")
			flags.instances = 1
		}

		if len(flags.ports) < flags.instances {
			missing := (flags.instances - len(flags.ports))

			log.Printf("not enough ports provided to fill instances assigned: %v, missing %v...\n", flags.ports, missing)
			ports, errors := tget.GetFreePorts(missing)
			if len(errors) > 0 {
				for _, e := range errors {
					log.Println(e)
				}
				log.Fatalln("failed to auto-discover free ports")
			}

			for _, port := range ports {
				log.Printf("found free port for SockPort: %v...\n", port)
				flags.ports = append(flags.ports, uint(port))
			}
		}
		log.Printf("using ports: %v\n", flags.ports)

		log.Printf("preparing %d instances of Tor...\n", flags.instances)
		var torrc string
		if flags.conf == "" {
			log.Printf("using default .torrc template\n")
			torrc = tget.TorrcTemplate
		} else {
			log.Printf("using provided .torrc template: %s\n", flags.conf)
			dat, err := os.ReadFile(flags.conf)
			if err != nil {
				log.Fatalf("failed to read .torrc template file: %s\n", flags.conf)
				return
			}
			torrc = string(dat)
		}

		var torswg sync.WaitGroup
		var tors []*tor.Tor
		var clients []*http.Client
		for i := 0; i < flags.instances; i++ {
			//Create temp dir and torrc files
			dir, err := os.MkdirTemp(os.TempDir(), "tget_*")
			if err != nil {
				log.Fatal(err)
			}
			defer os.RemoveAll(dir)
			file, err := os.Create(path.Join(dir, fmt.Sprintf("%d_%v.torrc", flags.ports[i], uuid.New().String())))
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("creating temp torrc file at: %s\n", file.Name())

			torConf := template.Must(template.New(file.Name()).Parse(torrc))
			err = torConf.Execute(file, struct {
				SocksPort   uint
				ControlPort uint
				DataDir     string
			}{
				SocksPort:   flags.ports[i],
				ControlPort: flags.ports[i] + 1, // TODO: we should also do the check if available here
				DataDir:     os.TempDir(),
			})
			if err != nil {
				log.Fatalln(err)
			}
			file.Seek(0, 0) //reset to start of file
			defer file.Close()

			tbProxyURL, err := url.Parse(fmt.Sprintf("%s://%s:%d", flags.socksVersion, flags.host, flags.ports[i]))
			if err != nil {
				log.Fatalf("failed to parse proxy URL: %v\n", err)
				continue
			}

			tbDialer, err := proxy.FromURL(tbProxyURL, proxy.Direct)
			if err != nil {
				log.Fatalf("failed to obtain proxy dialer: %v\n", err)
				continue
			}
			log.Println(tbDialer)

			tbTransport := &http.Transport{
				Dial: tbDialer.Dial,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: flags.unsafeTLS,
				},
			}
			clients = append(clients, &http.Client{Transport: tbTransport})

			var dialCtx = context.Background()
			var dialCancel context.CancelFunc
			if flags.maxWait > 0 {
				dialCtx, dialCancel = context.WithTimeout(context.Background(), time.Duration(flags.maxWait)*time.Second)
				defer dialCancel()
			}

			torInstance, err := tor.Start(dialCtx, &tor.StartConf{
				TorrcFile:         file.Name(),
				NoAutoSocksPort:   true,
				EnableNetwork:     true,
				RetainTempDataDir: false,
				NoHush:            flags.verbose,
			})
			if err != nil {
				log.Fatalf("failed to start Tor instance: %v\n", err)
				continue
			}

			tors = append(tors, torInstance)
			torswg.Add(1)
			go func(id int, c *http.Client) {
				defer torswg.Done()

				for {
					log.Printf("waiting for tor instance %v to start...\n", i)

					_, err := c.Get(flags.testDomain)
					if err != nil {
						time.Sleep(3 * time.Second)
						continue
					}

					log.Printf("instance %v ready!\n", i)
					break
				}

			}(i, clients[i])
		}

		//Wait for all tor instances to be ready
		torswg.Wait()
		log.Printf("created %d http Tor clients using %d Tor instances\n", len(clients), flags.instances)
		log.Println("Tor instances:", tors)

		//Get list of URLs
		urls := args
		if flags.fromFile {
			urls = []string{}
			for _, a := range args {
				data, err := os.ReadFile(a)
				if err != nil {
					log.Fatalf("failed to open %s: %v\n", a, err)
					continue
				}
				urls = strings.Split(string(data), "\n")
			}
		}

		//Wait for tors to go online

		log.Printf("total URLs: %d\n", len(urls))
		chunks := tget.ChunkBy(urls, flags.instances)
		log.Printf("chunks: %v\n", chunks)

		var wg sync.WaitGroup
		p, _ := ants.NewPool(flags.concurrency)

		bars := mpb.New(mpb.WithWaitGroup(&wg))

		//Feed chunks to workers
		for i := range flags.instances {
			log.Printf("instance %d will download %v URLs\n", i, len(chunks[i]))

			chunk := chunks[i]
			p.Submit(func() {
				//Worker
				for _, url := range chunk {
					wg.Add(1)

					go func(c *http.Client) {
						defer wg.Done()

						req, _ := http.NewRequest(flags.method, url, nil)

						//populate headers, cookies, etc
						for _, h := range flags.headers {
							split := strings.Split(h, "=")
							k := split[0]
							v := ""
							if len(split) > 1 {
								v = strings.Join(split[1:], "=")
							}

							req.Header.Add(k, v)
						}

						if flags.cookies != "" {
							req.Header.Add("Cookie", flags.cookies)
						}

						if len(flags.body) > 0 {
							req.Body = io.NopCloser(strings.NewReader(flags.body))
						}

						baseFileName := path.Base(req.URL.Path)
						if baseFileName == "." || baseFileName == "/" || baseFileName == "" {
							baseFileName = fmt.Sprintf("%v_index.html", req.URL.Host)
						}
						if !flags.ovewrite {
							baseFileName = tget.GetFilename(baseFileName, 0) // if path is / or "" we should save as index.html.<attempt>
						}

						outFilePath := path.Join(flags.outPath, baseFileName)
						log.Printf("client %d downloading %v to %v\n", i, url, outFilePath)

						currentSize := 0
						if stat, err := os.Stat(outFilePath); err == nil {
							// TODO: implement proper resume with etag/filehash/Ifrange etc, check if accpet-range is supported, etc
							if flags.tryContinue && !flags.ovewrite {
								currentSize := stat.Size()
								log.Printf("%v found on disk, user asked to attempt a resume (%d)\n", outFilePath, currentSize)
								req.Header.Set("range", fmt.Sprintf("%d-", currentSize))
							}

							//in case overwrite is set, start from the beginning anyway
							if flags.ovewrite {
								currentSize = 0
								req.Header.Del("range")
							}
						}

						out, err := os.OpenFile(outFilePath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
						if err != nil {
							log.Println(err)
							return
						}
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

						bar := bars.AddBar(
							int64(totalBytes),
							mpb.PrependDecorators(
								// simple name decorator
								decor.Name(baseFileName),
								// decor.DSyncWidth bit enables column width synchronization
								decor.Percentage(decor.WCSyncSpace),
							),
							mpb.AppendDecorators(
								// replace ETA decorator with "done" message, OnComplete event
								decor.OnComplete(
									// ETA decorator with ewma age of 30
									decor.EwmaETA(decor.ET_STYLE_GO, 30, decor.WCSyncWidth), "done",
								),
							),
						)

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

							bar.IncrInt64(int64(written))
						}
					}(clients[i])
				}

			})
		}

		bars.Wait()
		wg.Wait()

		log.Println("terminating Tor instances...")
		for _, t := range tors {
			defer t.Close()
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	//If tor already installed, use Tor's location as default tor path
	torPath := which.Which("tor")

	// General runtime flags
	rootCmd.Flags().StringVarP(&flags.conf, "conf", "c", "", ".torrc template file to use")
	rootCmd.Flags().UintSliceVarP(&flags.ports, "ports", "p", []uint{}, "ports to for Tor to listen on")
	rootCmd.Flags().BoolVarP(&flags.fromFile, "from-file", "F", false, "download from files instead of urls") // TODO: support URIs instead?
	rootCmd.Flags().IntVar(&flags.concurrency, "concurrency", 10, "concurrency level")
	rootCmd.Flags().IntVarP(&flags.instances, "instances", "n", 5, "number of Tor instances to use")
	rootCmd.Flags().StringVar(&flags.host, "host", "127.0.0.1", "host running Tor")
	rootCmd.Flags().StringVarP(&flags.torPath, "tor-path", "t", torPath, "path to Tor binary")
	rootCmd.Flags().StringVarP(&flags.logPath, "log-path", "l", "", "path to save logs at")
	rootCmd.Flags().StringVarP(&flags.socksVersion, "socks-version", "S", "socks5", "socks version to use")
	rootCmd.Flags().StringVar(&flags.testDomain, "test-domain", "https://thatsn0tmy.site", "website to use while testing if Tor is up")
	rootCmd.Flags().StringVarP(&flags.outPath, "out-path", "o", ".", "path to save downloaded files in")
	rootCmd.Flags().BoolVarP(&flags.verbose, "verbose", "v", false, "be (very) verbose")
	rootCmd.Flags().BoolVarP(&flags.ovewrite, "ovewrite", "O", false, "overwrite file(s) if they already exist")
	rootCmd.Flags().BoolVar(&flags.tryContinue, "continue", false, "attempt to continue a previously interrupted download")
	rootCmd.Flags().IntVarP(&flags.maxWait, "timeout", "T", 0, "max time to wait for Tor before canceling (0: no timeout)")

	// Headers, cookies, ssl, etc
	rootCmd.Flags().BoolVarP(&flags.unsafeTLS, "unsafe-tls", "k", false, "skip TLS certificates validation")
	rootCmd.Flags().StringSliceVarP(&flags.headers, "header", "H", []string{}, "header(s) to include in all requests")
	rootCmd.Flags().StringVarP(&flags.cookies, "cookies", "C", "", "cookie(s) to include in all requests")
	rootCmd.Flags().StringVarP(&flags.body, "data", "d", "", "body of request to send")

	rootCmd.Flags().StringVarP(&flags.method, "method", "X", "GET", "HTTP method to use")

}
