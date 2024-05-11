/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"text/template"
	"torget/tget"

	"github.com/hairyhenderson/go-which"
	"github.com/spf13/cobra"
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

	method    string
	cookies   []string
	headers   []string
	unsafeTLS bool
}

var flags tgetFlags

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "tget [flags] <url|file> [...url|file]",
	Short: "A (fast) Tor file downloader",
	Long:  `A file downloader which uses multiple tor instances to try to use all available bandwidth`,
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
			// TODO: in case we need A LOT of instances, this is a sequential check, could be made concurrent
			// TODO: ask OS for a free port, if TorHost is not localhost this is not always true, hence fail
			log.Fatalf("not enough ports assigned: %v...\n", flags.ports)
			return
			/*for i := 0; i < (flags.instances - len(flags.ports)); i++ {
				addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:0", flags.host))
				if err != nil {
					log.Fatalf("can't resolve: %v\n", flags.host)
				}
				if !addr.IP.Equal(net.ParseIP("127.0.0.1")) {
					log.Fatalf("can't discover available ports on Tor host %s, not enough ports specified: %v\n", flags.host, flags.ports)
				}
				tmp, err := net.ListenTCP("tcp", addr)
				if err != nil {
					log.Fatalf("couldn't check port: %d\n", tmp.Addr().(*net.TCPAddr).Port)
				}
				defer tmp.Close()

				// this port was just available, mark as usable (techinically someone could steal it meanwhile, we could reserve it...but meh)
				port := (uint)(tmp.Addr().(*net.TCPAddr).Port)

				log.Printf("not enough ports assigned, using available random port: %d...\n", port)

				flags.ports = append(flags.ports, port) // hopefully OS is smart enough to allocate a random available port for us
			}*/
		}

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

		//var tors []*tor.Tor
		var clients []*http.Client
		for i := 0; i < flags.instances; i++ {
			//Create temp dir and torrc files
			dir, err := os.MkdirTemp(os.TempDir(), "tget_*")
			if err != nil {
				log.Fatal(err)
			}
			defer os.RemoveAll(dir)
			file, err := os.Create(path.Join(dir, fmt.Sprintf("%d.torrc", flags.ports[i])))
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("creating temp torrc file at: %s\n", file.Name())

			torConf := template.Must(template.New(file.Name()).Parse(torrc))
			torConf.Execute(file, struct {
				socksport   uint
				controlport uint
				data_dir    string
			}{
				socksport:   flags.ports[i],
				controlport: flags.ports[i] + 1, // TODO: we should also do the check if available here
				data_dir:    os.TempDir(),
			})

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
			tbTransport := &http.Transport{Dial: tbDialer.Dial}
			clients = append(clients, &http.Client{Transport: tbTransport})
		}
		log.Printf("created %d http Tor clients using %d Tor instances\n", len(clients), flags.instances)

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

		log.Printf("total URLs: %d\n", len(urls))
		chunks := tget.ChunkBy(urls, len(urls)/flags.instances)
		for i, c := range chunks {
			log.Printf("instance %d will download %v URLs\n", i, len(c))
		}

		//var wg sync.WaitGroup
		//TODO: split in chunks, assign chunks to flags.concurrency goroutines
		//		before starting download check if file with same name already exists
		//		if so try resuming it

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
	rootCmd.Flags().StringVarP(&flags.outPath, "out-path", "o", ".", "path to save downloaded files in")
	//TODO: verbosity

	// Headers, cookies, ssl, etc
	rootCmd.Flags().BoolVarP(&flags.unsafeTLS, "unsafe-tls", "k", false, "skip TLS certificates validation")
	rootCmd.Flags().StringSliceVarP(&flags.headers, "header", "H", []string{}, "header(s) to include in all requests")
	rootCmd.Flags().StringSliceVarP(&flags.cookies, "cookie", "C", []string{}, "cookie(s) to include in all requests")
	rootCmd.Flags().StringVarP(&flags.method, "method", "X", "GET", "HTTP method to use")

}
