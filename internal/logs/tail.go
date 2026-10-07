// Package logs sigue ficheros de log y los colorea por nivel.
package logs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// StartOffset devuelve el offset donde empiezan las últimas n líneas.
// Lee el fichero hacia atrás por bloques, así que es instantáneo aunque el
// catalina.out pese varios GB.
func StartOffset(path string, n int) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	size := st.Size()
	if n <= 0 {
		return size
	}
	const chunk = 64 * 1024
	buf := make([]byte, chunk)
	lines := 0
	for pos := size; pos > 0; {
		read := int64(chunk)
		if pos < read {
			read = pos
		}
		pos -= read
		if _, err := f.ReadAt(buf[:read], pos); err != nil && !errors.Is(err, io.EOF) {
			return 0
		}
		for i := read - 1; i >= 0; i-- {
			if buf[i] != '\n' || pos+i == size-1 {
				continue
			}
			lines++
			if lines == n {
				return pos + i + 1
			}
		}
	}
	return 0
}

// Follow emite cada línea completa del fichero a partir de offset y se queda
// esperando nuevas hasta que ctx se cancele. Tolera que el fichero aún no
// exista, que se trunque o que se rote. flush se llama tras cada ráfaga para
// que quien escribe pueda volcar su buffer de una vez (mucho más eficiente que
// escribir línea a línea en la terminal).
func Follow(ctx context.Context, path string, offset int64, onLine func(string), flush func()) error {
	var (
		f       *os.File
		reader  *bufio.Reader
		pos     = offset
		partial []byte
	)
	defer func() {
		if f != nil {
			f.Close()
		}
	}()

	open := func() bool {
		ff, err := os.Open(path)
		if err != nil {
			return false
		}
		if st, err := ff.Stat(); err == nil && st.Size() < pos {
			pos = 0 // lo han truncado mientras no mirábamos
		}
		if _, err := ff.Seek(pos, io.SeekStart); err != nil {
			ff.Close()
			return false
		}
		f, reader = ff, bufio.NewReaderSize(ff, 256*1024)
		return true
	}

	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		if f != nil || open() {
			for {
				chunk, err := reader.ReadSlice('\n')
				pos += int64(len(chunk))
				if len(chunk) > 0 {
					if err == nil {
						line := append(partial, chunk...)
						onLine(trimEOL(line))
						partial = partial[:0]
					} else {
						partial = append(partial, chunk...)
					}
				}
				if err != nil {
					if errors.Is(err, bufio.ErrBufferFull) {
						continue
					}
					break
				}
			}
			flush()
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		if f == nil {
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			continue // rotación en curso: el fichero volverá a aparecer
		}
		cur, err := f.Stat()
		if err != nil || !os.SameFile(st, cur) || st.Size() < pos {
			f.Close()
			f, reader, pos, partial = nil, nil, 0, partial[:0]
		}
	}
}

func trimEOL(b []byte) string {
	n := len(b)
	for n > 0 && (b[n-1] == '\n' || b[n-1] == '\r') {
		n--
	}
	return string(b[:n])
}

// FollowReader emite las líneas que llegan por r (por ejemplo, un tail en otra
// máquina) hasta que se acaba. Cada lectura es una ráfaga: tras ella se llama
// a flush. Para cortarlo antes, quien llama cierra r.
func FollowReader(ctx context.Context, r io.Reader, onLine func(string), flush func()) error {
	buf := make([]byte, 64*1024)
	var partial []byte
	for {
		n, err := r.Read(buf)
		data := buf[:n]
		for len(data) > 0 {
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				partial = append(partial, data...)
				break
			}
			line := data[:i+1]
			if len(partial) > 0 {
				partial = append(partial, line...)
				line = partial
			}
			onLine(trimEOL(line))
			partial = partial[:0]
			data = data[i+1:]
		}
		if n > 0 {
			flush()
		}
		if err != nil {
			if len(partial) > 0 {
				onLine(trimEOL(partial))
				flush()
			}
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// Source produce líneas de log hasta que ctx se cancela o se acaban.
type Source func(ctx context.Context, onLine func(string), flush func()) error

// FileSource sigue un fichero local desde offset (ver Follow).
func FileSource(path string, offset int64) Source {
	return func(ctx context.Context, onLine func(string), flush func()) error {
		return Follow(ctx, path, offset, onLine, flush)
	}
}
