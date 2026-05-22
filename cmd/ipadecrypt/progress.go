package main

import (
	"io"
	"time"
)

const progressTick = 100 * time.Millisecond

type progressReader struct {
	r          io.Reader
	total      int64
	read       int64
	last       time.Time
	onProgress func(cur, total int64)
}

func newProgressReader(r io.Reader, total int64, onProgress func(cur, total int64)) *progressReader {
	return &progressReader{r: r, total: total, onProgress: onProgress}
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.read += int64(n)
		if now := time.Now(); now.Sub(p.last) >= progressTick {
			p.last = now
			p.onProgress(p.read, p.total)
		}
	}

	if err == io.EOF {
		p.onProgress(p.read, p.total)
	}

	return n, err
}

// countingWriter wraps an io.Writer and tallies bytes written. onTick, if
// set, fires at most every progressTick with the running byte count  used
// to surface streaming progress when the total size isn't known up front.
type countingWriter struct {
	w      io.Writer
	n      int64
	last   time.Time
	onTick func(n int64)
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)

	c.n += int64(n)
	if c.onTick != nil && n > 0 {
		if now := time.Now(); now.Sub(c.last) >= progressTick {
			c.last = now
			c.onTick(c.n)
		}
	}

	return n, err
}
