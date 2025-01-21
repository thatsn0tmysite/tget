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
	"regexp"
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
	socksVersion   string
	conf           string
	ports          []uint
	fromFile       bool
	concurrency    int
	instances      int
	getTor         bool
	torPath        string
	logPath        string
	outPath        string
	host           string
	verbose        bool
	maxWait        int
	overwrite      bool
	tryContinue    bool
	testDomain     string
	keepalive      bool
	prepare        bool
	reuseinstances bool
	partial        bool

	body           string
	method         string
	cookies        string
	headers        []string
	useragent      string
	unsafeTLS      bool
	followRedirect bool
}

var flags tgetFlags

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "tget [flags] <url|file> [...url|file]",
	Short: "A (fast) Tor file downloader",
	Long: `Torget is a Tor aware file downloader which uses multiple Tor instances to try to use all available bandwidth.
	Made by thatsn0tmysite (aka n0tme) | Blog: https://thatsn0tmy.site`,
	Run: func(cmd *cobra.Command, args []string) {
		//Setup logger
		if flags.logPath != "" {
			logFile, err := os.Create(flags.logPath)
			if err != nil {
				log.Fatalf("error creating logfile: %s\n", err)
			}

			if flags.verbose {
				mw := io.MultiWriter(os.Stdout, logFile)
				log.SetOutput(mw)
			} else {
				log.SetOutput(logFile)
			}
		}

		if len(args) < 1 {
			fmt.Println("No urls or files specified!")
			cmd.Root().Help()
			return
		}

		//Imply keepalive if we are reusing instances
		if flags.reuseinstances {
			flags.keepalive = true
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

			if flags.verbose {
				log.Printf("not enough ports provided to fill instances assigned: %v, missing %v...\n", flags.ports, missing)
			}
			ports, errors := tget.GetFreePorts(missing)
			if len(errors) > 0 {
				for _, e := range errors {
					log.Println(e)
				}
				log.Fatalln("failed to auto-discover free ports")
			}

			for _, port := range ports {
				if flags.verbose {
					log.Printf("found free port for SockPort: %v...\n", port)
				}
				flags.ports = append(flags.ports, uint(port))
			}
		}

		if flags.verbose {
			log.Printf("using ports: %v\n", flags.ports)
			log.Printf("preparing %d instances of Tor...\n", flags.instances)
		}
		var torrc string
		if flags.conf == "" {
			if flags.verbose {
				log.Printf("using default .torrc template\n")
			}
			torrc = tget.TorrcTemplate
		} else {
			if flags.verbose {
				log.Printf("using provided .torrc template: %s\n", flags.conf)
			}
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

		torProgress := mpb.New(mpb.WithWaitGroup(&torswg))
		torBar := torProgress.New(int64(flags.instances),
			mpb.BarStyle(),
			mpb.PrependDecorators(
				decor.Name("Starting Tor instances"),
			),
			mpb.AppendDecorators(decor.Percentage()),
		)

		for i := 0; i < flags.instances; i++ {
			//Create temp dir and torrc files
			dir, err := os.MkdirTemp(os.TempDir(), "tget_*")
			if err != nil {
				log.Fatal(err)
			}
			if !flags.keepalive {
				defer os.RemoveAll(dir)
			}

			file, err := os.Create(path.Join(dir, fmt.Sprintf("%d_%v.torrc", flags.ports[i], uuid.New().String())))
			if err != nil {
				log.Fatal(err)
			}
			if flags.verbose {
				log.Printf("creating temp torrc file at: %s\n", file.Name())
			}

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
			if flags.verbose {
				log.Println("tbDialer:", tbDialer)
			}

			tbTransport := &http.Transport{
				Dial: tbDialer.Dial,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: flags.unsafeTLS,
				},
				DisableCompression: true,
			}
			clients = append(clients, &http.Client{Transport: tbTransport})

			var dialCtx = context.Background()
			var dialCancel context.CancelFunc
			if flags.maxWait > 0 {
				dialCtx, dialCancel = context.WithTimeout(context.Background(), time.Duration(flags.maxWait)*time.Second)
				defer dialCancel()
			}

			if !flags.reuseinstances { // we are spawning the instances
				torInstance, err := tor.Start(dialCtx, &tor.StartConf{
					ExePath:           flags.torPath,
					TorrcFile:         file.Name(),
					NoAutoSocksPort:   true,
					EnableNetwork:     true,
					RetainTempDataDir: flags.keepalive,
					NoHush:            flags.verbose,
				})
				if err != nil {
					log.Fatalf("failed to start Tor instance: %v\n", err)
					continue
				}

				tors = append(tors, torInstance)
			}
			torswg.Add(1)

			go func(id int, c *http.Client, progress *mpb.Bar) {
				defer torswg.Done()

				for {
					if flags.verbose {
						log.Printf("waiting for tor instance %v to start...\n", i)
					}

					_, err := c.Get(flags.testDomain)
					if err != nil {
						time.Sleep(3 * time.Second)
						continue
					}

					if flags.verbose {
						log.Printf("instance %v ready!\n", i)
					}
					break
				}

				progress.IncrBy(1)
			}(i, clients[i], torBar)
		}

		//Wait for all tor instances to be ready
		//torswg.Wait()
		torProgress.Wait()
		if flags.verbose && flags.keepalive {
			log.Printf("created %d http Tor clients using %d Tor instances\n", len(clients), flags.instances)
			log.Println("Tor instances:", tors)
		}
		if flags.prepare {
			return
		}

		//Get list of URLs
		isEmptyRegex, _ := regexp.Compile(`^\s*$`)

		urls := args
		if flags.fromFile {
			urls = []string{}
			for _, a := range args {
				data, err := os.ReadFile(a)
				if err != nil {
					log.Fatalf("failed to open %s: %v\n", a, err)
					continue
				}
				urls = []string{}

				for _, url := range strings.Split(string(data), "\n") {
					if !isEmptyRegex.MatchString(url) {
						urls = append(urls, url)
					}
				}

			}
		}

		var partialDownloads [][][]int64
		var fullDownloads [][]string
		if flags.partial {
			partialDownloads, fullDownloads = tget.SplitAndDistributeDownloads(clients, urls, flags.instances, flags.headers, flags.cookies, flags.useragent, flags.body, flags.followRedirect)
		} else {
			fullDownloads = tget.ChunkBy(urls, flags.instances)
		}

		if flags.verbose {
			log.Printf("total URLs: %d\n", len(urls))
			log.Printf("partialDownloads: %v\n", partialDownloads)
			log.Printf("fullDownloads: %v\n", fullDownloads)
		}

		var wg sync.WaitGroup

		//p, _ := ants.NewPool(flags.concurrency)

		bars := mpb.New(mpb.WithWaitGroup(&wg))

		startTime := time.Now()

		splitMap := make(map[string][]string)

		//Feed chunks to workers
		for i := range flags.instances {
			p, _ := ants.NewPool(flags.concurrency) // each "instance"/chunk gets a pool

			var partialDownloadsChunk [][]int64
			var fullDownloadsChunk []string
			if len(partialDownloads) > 0 && i < len(partialDownloads) {
				partialDownloadsChunk = partialDownloads[i]
			}

			if len(fullDownloads) > 0 && i < len(fullDownloads) {
				fullDownloadsChunk = fullDownloads[i]
			}

			if flags.verbose {
				log.Printf("instance %d will download %v URLs\n", i, len(fullDownloadsChunk))
			}

			for j, rng := range partialDownloadsChunk {
				wg.Add(1)

				req, _ := http.NewRequest(flags.method, urls[j], nil)
				tget.PrepareRequest(req, flags.headers, flags.cookies, flags.useragent, flags.body)

				baseFileName := path.Base(req.URL.Path)
				if baseFileName == "." || baseFileName == "/" {
					baseFileName = fmt.Sprintf("%v_index.html", req.URL.Host)
					if flags.verbose {
						log.Printf("instance %d will download %v (part%d) (%v)\n", i, baseFileName, i, req)
					}
				}
				originalFileName := baseFileName
				baseFileName = fmt.Sprintf("%s.part%d", baseFileName, i)
				outFilePath := path.Join(flags.outPath, baseFileName)
				err = os.MkdirAll(flags.outPath, os.ModePerm)
				if err != nil {
					log.Fatalf("failed to create directory: %v\n", err)
					return
				}

				if !flags.tryContinue && !flags.overwrite {
					outFilePath = tget.GetFilename(outFilePath) // if path is / or "" we should save as index.html.<attempt>
				}
				splitMap[originalFileName] = append(splitMap[originalFileName], outFilePath)

				//TODO: rework to make it look better maybe: filenam [bar] [ETA] [status/percentage]?
				bar := bars.AddBar(
					0,
					mpb.PrependDecorators(
						decor.Name(fmt.Sprintf("[T%d] %v", i, baseFileName)),
					),
					mpb.AppendDecorators(
						//decor.Counters(decor.SizeB1024(0), "% .1f / % .1f"),
						decor.EwmaSpeed(decor.SizeB1024(0), " % .2f ", 60),
						decor.Name("|"),
						decor.Total(decor.SizeB1024(0), " %d "),
						decor.Name("|"),
						decor.Percentage(decor.WCSyncSpace),
						decor.Name("|"),
						decor.OnAbort(
							decor.AverageETA(decor.ET_STYLE_GO, decor.WCSyncWidth), "aborted",
						),
					),
				)

				p.Submit(func() {
					defer wg.Done()
					tget.PartialDownloadUrl(
						clients[i],
						req,
						outFilePath,
						rng[0],
						rng[1],
						flags.followRedirect,
						flags.tryContinue,
						flags.overwrite,
						bar,
					)
				})
			}

			for _, url := range fullDownloadsChunk {
				wg.Add(1)

				req, _ := http.NewRequest(flags.method, url, nil)
				tget.PrepareRequest(req, flags.headers, flags.cookies, flags.useragent, flags.body)

				baseFileName := path.Base(req.URL.Path)
				if baseFileName == "." || baseFileName == "/" {
					baseFileName = fmt.Sprintf("%v_index.html", req.URL.Host)
					if flags.verbose {
						log.Printf("instance %d will download %v URLs\n", i, req.URL.Host)
						log.Printf("instance %d will download %v (%v)\n", i, baseFileName, req)
					}
				}

				outFilePath := path.Join(flags.outPath, baseFileName)
				err = os.MkdirAll(flags.outPath, os.ModePerm)
				if err != nil {
					log.Fatalf("failed to create directory: %v\n", err)
					return
				}

				if !flags.tryContinue && !flags.overwrite {
					outFilePath = tget.GetFilename(outFilePath) // if path is / or "" we should save as index.html.<attempt>
				}

				//TODO: rework to make it look better maybe: filenam [bar] [ETA] [status/percentage]?
				bar := bars.AddBar(
					0,
					mpb.PrependDecorators(
						decor.Name(fmt.Sprintf("[T%d] %v", i, baseFileName)),
					),
					mpb.AppendDecorators(
						//decor.Counters(decor.SizeB1024(0), "% .1f / % .1f"),
						decor.EwmaSpeed(decor.SizeB1024(0), " % .2f ", 60),
						decor.Name("|"),
						decor.Total(decor.SizeB1024(0), " %d "),
						decor.Name("|"),
						decor.Percentage(decor.WCSyncSpace),
						decor.Name("|"),
						decor.OnAbort(
							decor.AverageETA(decor.ET_STYLE_GO, decor.WCSyncWidth), "aborted",
						),
					),
				)

				p.Submit(func() {
					defer wg.Done()
					tget.DownloadUrl(clients[i], req, outFilePath, flags.followRedirect, flags.tryContinue, flags.overwrite, bar)
				})
			}
		}
		bars.Wait()

		if flags.partial && len(splitMap) > 0 {
			for outFilePath, splitFiles := range splitMap {
				err := tget.MergeChunkFiles(splitFiles, outFilePath)
				if err != nil {
					fmt.Printf("merging splits for %s: %v\n", outFilePath, err)
				} else {
					for _, f := range splitFiles {
					    os.Remove(f)
					}
				}
			}
		}

		if flags.verbose {
			log.Println("downloads of", len(urls), "files took: ", (time.Since(startTime)))
		}

		if !flags.keepalive {
			log.Println("terminating Tor instances...")
			for _, t := range tors {
				defer t.Close()
			}
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
	cwd, _ := os.Getwd()

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
	rootCmd.Flags().StringVarP(&flags.outPath, "out-path", "o", cwd, "path to save downloaded files in")
	rootCmd.Flags().BoolVarP(&flags.verbose, "verbose", "v", false, "be (very) verbose")
	rootCmd.Flags().BoolVarP(&flags.overwrite, "overwrite", "O", false, "overwrite file(s) if they already exist")
	rootCmd.Flags().BoolVar(&flags.tryContinue, "continue", false, "attempt to continue a previously interrupted download")
	rootCmd.Flags().IntVar(&flags.maxWait, "timeout", 0, "max time to wait for Tor before canceling (0: no timeout)")
	rootCmd.Flags().BoolVar(&flags.keepalive, "keep-alive", false, "do not close Tor instances when done")
	rootCmd.Flags().BoolVar(&flags.prepare, "prepare", false, "only start the requested Tor instances and quit (implies --keep-alive)")

	rootCmd.Flags().BoolVarP(&flags.reuseinstances, "reuse-instances", "R", false, "do not spawn new instances, assume they are already open (implies --keep-alive)")

	rootCmd.Flags().BoolVar(&flags.partial, "partial", false, "attempt to split and distribute each file part in Tor instances")

	// Headers, cookies, ssl, etc
	rootCmd.Flags().BoolVarP(&flags.followRedirect, "follow-redirect", "f", false, "follow HTTP redirects")
	rootCmd.Flags().BoolVarP(&flags.unsafeTLS, "unsafe-tls", "k", false, "skip TLS certificates validation")
	rootCmd.Flags().StringArrayVarP(&flags.headers, "header", "H", []string{}, "header(s) to include in all requests")
	rootCmd.Flags().StringVarP(&flags.cookies, "cookies", "b", "", "cookie(s) to include in all requests")
	rootCmd.Flags().StringVarP(&flags.body, "data", "d", "", "body of request to send")
	rootCmd.Flags().StringVarP(&flags.useragent, "useragent", "U", fmt.Sprintf("tget/%v", tget.Version), "useraget to use when sending requests")

	rootCmd.Flags().StringVarP(&flags.method, "method", "X", "GET", "HTTP method to use")

}
