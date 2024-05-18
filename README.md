![torget logo](.images/logo.png "torget by thatsn0tmysite")

# Torget (tget)

Torget, or `tget` for short, is a http(s) file downloader for Tor made by [thatsn0tmysite](https://thatsn0tmy.site) (a.k.a. n0tme). 

Where `tget` "shines" over other tools is its (ab)use of bandwith, it spawns multiple Tor instances, it allows downloads of multiple files over multiple Tor clients, avoiding saturating the bandwith with concurrent downloads. 

Not the most novel nor elegant technique but... meh.

This allows for faster parallel downloads and a more total (theorical) bandwith.

> "Great for your daily dataleaks dumps (or anything else you use Tor for!)."
> - Someone's mom, probably.

This tool makes use of the handy [bine](https://github.com/cretz/bine) library and the fancy [mpb](https://github.com/vbauerster/mpb) library!

If you find this thing useful leave me a star or contribute, and consider [donating to Tor too](https://donate.torproject.org/)!

## Current features / TODOs
- [x] Basic functionality (multiple tor instances spawning)
- [x] Download from URLs or files
- [x] Allow download resume
- [x] Custom headers/cookies
- [x] Fancy progress bars
- [ ] Better/moar colors logging (use Tor colors)
- [x] Onion themed logo
- [ ] Refactor code out of `cmd/root.go` (**SOME** code refactored)
- [ ] Tests
- [ ] Insert fancy benchmark comparison gif to readme


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

# Contribution
Any contribution is welcome, so feel free to open issues and suggest features/fixes. 
Also, as usual: "Sorry for the messy code"... I'll clean it up eventually.