package tget

import (
	_ "embed"
	"fmt"
	"net"
	"os"
)

//go:embed torrc
var TorrcTemplate string

func GetFilename(file string, attempt int) string {
	if _, err := os.Stat(file); err == os.ErrNotExist {
		return file // valid filename, return
	} else if err == nil {
		return GetFilename(fmt.Sprintf("%s.%d", file, attempt), attempt+1)
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
