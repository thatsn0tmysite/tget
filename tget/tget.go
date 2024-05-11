package tget

import (
	_ "embed"

	"github.com/cretz/bine/tor"
)

//go:embed torrc
var TorrcTemplate string

type tget struct {
	instances []*tor.Tor
	torrc     string
	urls      []string
}

func ChunkBy[T any](items []T, chunkSize int) (chunks [][]T) {
	for chunkSize < len(items) {
		items, chunks = items[chunkSize:], append(chunks, items[0:chunkSize:chunkSize])
	}
	return append(chunks, items)
}

func (t *tget) NewTGet() tget {
	return tget{}
}

func (t *tget) Download() tget {
	return tget{}
}

func (t *tget) CheckResume() tget {
	return tget{}
}

func (*tget) Progress() tget {
	return tget{}
}
