//go:build unix

package scip

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// mapFile memory-maps path for read-only access. The returned cleanup func
// must be called when the data is no longer needed (after all proto.Unmarshal
// calls that reference it have completed). On Unix this avoids copying the
// file bytes onto the Go heap — the OS manages paging.
func mapFile(path string) (data []byte, cleanup func(), err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}

	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("stat: %w", err)
	}

	size := fi.Size()
	if size == 0 {
		f.Close()
		return []byte{}, func() {}, nil
	}

	fd := int(f.Fd()) // #nosec G115 -- fd fits in int
	data, err = unix.Mmap(fd, 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	f.Close() // fd can be closed immediately after Mmap
	if err != nil {
		return nil, nil, fmt.Errorf("mmap: %w", err)
	}

	return data, func() { unix.Munmap(data) }, nil //nolint:errcheck
}
