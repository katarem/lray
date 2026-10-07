package logs

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestFollowReader(t *testing.T) {
	r := &chunkReader{chunks: []string{"uno\ndo", "s\r\ntres\n", "cuatro"}}
	var lines []string
	flushes := 0
	err := FollowReader(context.Background(), r, func(l string) { lines = append(lines, l) }, func() { flushes++ })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "|"); got != "uno|dos|tres|cuatro" {
		t.Errorf("lines = %q", got)
	}
	if flushes < 3 {
		t.Errorf("flush tras cada ráfaga: %d", flushes)
	}
}

// chunkReader devuelve cada trozo en una lectura distinta, como una tubería.
type chunkReader struct{ chunks []string }

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[0])
	c.chunks = c.chunks[1:]
	return n, nil
}
