![torget logo](.images/logo.png "torget by thatsn0tmysite")

#Torget (tget)

Torget, or `tget` for short, is a http file downloader for Tor.

Where `tget` shines over other tools is its (ab)use of bandwith, spawning multiple Tor instances, it allows downloads of multiple files over multiple Tor clients, avoiding saturating the bandwith with concurrent downloads. 

This allows for faster parallel downloads and a more total badwith.

Great for your daily dataleaks downloads!

Here is a quick comparison of a single Tor instance with parallel downloads vs tget:

TODO: insert fancy gif here


This tool makes use of the handy bine library...

## Features / TODO
- [x] Basic functionality (multiple tor instances spawning)
- [x] Download from URLs or files
- [x] Allow download resume
- [x] Custom headers/cookies
- [x] Fancy progress bars
- [ ] Better logging
- [x] Onion themed logo
- [ ] Refactor code out of `cmd/root.go`
- [ ] Tests


## Usage
```
A file downloader which uses multiple Tor instances to try to use all available bandwidth

Usage:
  tget [flags] <url|file> [...url|file]

Flags:
      --concurrency int        concurrency level (default 10)
  -c, --conf string            .torrc template file to use
  -C, --cookie strings         cookie(s) to include in all requests
  -F, --from-file              download from files instead of urls
  -H, --header strings         header(s) to include in all requests
  -h, --help                   help for tget
      --host string            host running Tor (default "127.0.0.1")
  -n, --instances int          number of Tor instances to use (default 5)
  -l, --log-path string        path to save logs at
  -X, --method string          HTTP method to use (default "GET")
  -o, --out-path string        path to save downloaded files in (default ".")
  -p, --ports uints            ports to for Tor to listen on (default [])
  -S, --socks-version string   socks version to use (default "socks5")
  -t, --tor-path string        path to Tor binary (default "/usr/bin/tor")
  -k, --unsafe-tls             skip TLS certificates validation

```