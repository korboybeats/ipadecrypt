package assets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	remoteReadAttempts = 3
	// remoteBlockSize is the cache and fetch alignment. AEA cluster headers
	// are small and hundreds of megabytes apart; a 2 MiB sliding window was
	// pulling ~879 MiB of range traffic to build a 167 MiB cache. 64 KiB
	// covers a header (and a typical APFS block run start) without dragging
	// in the rest of the cluster payload.
	remoteBlockSize = 64 << 10
	// remoteCacheLimit keeps recently used blocks so payload that happens to
	// share a block with a header is not re-fetched, and APFS walks do not
	// re-download metadata.
	remoteCacheLimit = 64 << 20
	// remoteReadAhead is appended only when the next read continues a
	// sequential run, so scattered header walks stay small.
	remoteReadAhead    = 256 << 10
	remoteMaxParallel  = 4
	remoteRetryBackoff = 200 * time.Millisecond
	progressMinGap     = 250 * time.Millisecond // ~4 events/sec
)

type remoteFile struct {
	ctx    context.Context
	client *http.Client
	url    string
	size   int64

	mu         sync.Mutex
	blocks     map[int64][]byte
	lru        []int64
	cacheBytes int64
	fetched    int64
	needed     []span
	lastEnd    int64
	lastReport time.Time
	reporter   ProgressFunc
}

type span struct {
	start, end int64 // end exclusive
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          8,
			MaxIdleConnsPerHost:   remoteMaxParallel,
			MaxConnsPerHost:       remoteMaxParallel,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
			DisableCompression:    true,
		},
	}
}

//nolint:wsl // Request construction and validation are clearer in protocol order.
func openRemoteFile(ctx context.Context, client *http.Client) (*remoteFile, error) {
	return openRemoteFileWithProgress(ctx, client, nil)
}

func openRemoteFileWithProgress(ctx context.Context, client *http.Client, reporter ProgressFunc) (*remoteFile, error) {
	if client == nil {
		client = newHTTPClient()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, macOSRestoreURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create remote image request: %w", err)
	}

	request.Header.Set("Range", "bytes=0-0")
	response, err := client.Do(request)

	if err != nil {
		return nil, fmt.Errorf("open remote image: %w", err)
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("open remote image: server returned %s without byte-range support", response.Status)
	}

	start, end, size, err := parseContentRange(response.Header.Get("Content-Range"))
	if err != nil {
		return nil, fmt.Errorf("open remote image: %w", err)
	}
	if start != 0 || end != 0 {
		return nil, fmt.Errorf("open remote image: server returned unexpected byte range %d-%d", start, end)
	}

	return &remoteFile{
		ctx:      ctx,
		client:   client,
		url:      macOSRestoreURL,
		size:     size,
		blocks:   make(map[int64][]byte),
		reporter: reporter,
	}, nil
}

//nolint:wsl // Parsing checks are clearer in wire-format order.
func parseContentRange(value string) (int64, int64, int64, error) {
	unitAndRange, total, ok := strings.Cut(value, "/")
	if !ok || total == "" || total == "*" {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range %q", value)
	}
	size, err := strconv.ParseInt(total, 10, 64)
	if err != nil || size <= 0 {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range %q", value)
	}
	rangeValue, ok := strings.CutPrefix(unitAndRange, "bytes ")
	if !ok {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range %q", value)
	}

	startValue, endValue, ok := strings.Cut(rangeValue, "-")
	if !ok {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range %q", value)
	}
	start, startErr := strconv.ParseInt(startValue, 10, 64)
	end, endErr := strconv.ParseInt(endValue, 10, 64)
	if startErr != nil || endErr != nil || start < 0 || end < start || end >= size {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range %q", value)
	}

	return start, end, size, nil
}

func alignBlock(offset int64) int64 {
	if offset < 0 {
		return 0
	}
	return offset - offset%remoteBlockSize
}

// ReadAt serves reads from an aligned block cache. Scattered metadata walks
// fetch only the touched blocks; sequential payload walks read ahead.
func (r *remoteFile) ReadAt(data []byte, offset int64) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if offset < 0 {
		return 0, errors.New("negative remote image offset")
	}
	if offset >= r.size {
		return 0, io.EOF
	}

	wanted := len(data)
	if remaining := r.size - offset; int64(wanted) > remaining {
		wanted = int(remaining)
	}

	if err := r.fill(offset, int64(wanted)); err != nil {
		return 0, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.needed = addSpan(r.needed, offset, offset+int64(wanted))

	copied := 0
	for copied < wanted {
		blockOff := alignBlock(offset + int64(copied))
		block, ok := r.blocks[blockOff]
		if !ok {
			return copied, fmt.Errorf("remote block cache miss at offset %d", offset+int64(copied))
		}
		rel := int(offset + int64(copied) - blockOff)
		if rel < 0 || rel >= len(block) {
			return copied, fmt.Errorf("remote block cache miss at offset %d", offset+int64(copied))
		}
		copied += copy(data[copied:wanted], block[rel:])
	}

	if wanted != len(data) {
		return copied, io.EOF
	}
	return copied, nil
}

func (r *remoteFile) fill(offset, length int64) error {
	r.mu.Lock()

	end := offset + length
	if end > r.size {
		end = r.size
	}
	sequential := r.lastEnd > 0 && offset <= r.lastEnd+remoteBlockSize && offset >= r.lastEnd-remoteBlockSize
	fetchEnd := end
	if sequential {
		fetchEnd += remoteReadAhead
	}
	if fetchEnd > r.size {
		fetchEnd = r.size
	} else if rem := fetchEnd % remoteBlockSize; rem != 0 {
		fetchEnd += remoteBlockSize - rem
		if fetchEnd > r.size {
			fetchEnd = r.size
		}
	}
	r.lastEnd = end

	// Do not issue read-ahead GETs when the requested range is already cached.
	// Prefetch only when this read itself misses.
	if r.cachedLocked(offset, end) {
		r.mu.Unlock()
		return nil
	}

	missing := r.missingRangesLocked(alignBlock(offset), fetchEnd)
	if len(missing) == 0 {
		r.mu.Unlock()
		return nil
	}

	type result struct {
		start int64
		data  []byte
		err   error
	}

	jobs := missing
	results := make([]result, len(jobs))
	workers := remoteMaxParallel
	if workers > len(jobs) {
		workers = len(jobs)
	}

	// Release the cache lock for the duration of the HTTP GETs.
	r.mu.Unlock()

	var wg sync.WaitGroup
	jobsCh := make(chan int, len(jobs))
	for i := range jobs {
		jobsCh <- i
	}
	close(jobsCh)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobsCh {
				start, end := jobs[idx].start, jobs[idx].end
				buf := make([]byte, end-start)
				err := r.fetchRange(buf, start)
				results[idx] = result{start: start, data: buf, err: err}
			}
		}()
	}
	wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()

	var firstErr error
	for _, res := range results {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		r.installLocked(res.start, res.data)
	}
	r.reportProgressLocked()
	return firstErr
}

func (r *remoteFile) cachedLocked(offset, end int64) bool {
	if offset < 0 || end > r.size || offset >= end {
		return false
	}
	for off := offset; off < end; {
		blockOff := alignBlock(off)
		block, ok := r.blocks[blockOff]
		if !ok {
			return false
		}
		rel := off - blockOff
		if rel < 0 || rel >= int64(len(block)) {
			return false
		}
		off += int64(len(block)) - rel
	}
	return true
}

func (r *remoteFile) missingRangesLocked(start, end int64) []span {
	if start < 0 {
		start = 0
	}
	if end > r.size {
		end = r.size
	}
	if start >= end {
		return nil
	}

	var missing []span
	var runStart int64 = -1
	for off := alignBlock(start); off < end; off += remoteBlockSize {
		if _, ok := r.blocks[off]; ok {
			if runStart >= 0 {
				missing = append(missing, span{start: runStart, end: off})
				runStart = -1
			}
			continue
		}
		if runStart < 0 {
			runStart = off
		}
	}
	if runStart >= 0 {
		last := alignBlock(end - 1)
		runEnd := last + remoteBlockSize
		if runEnd > r.size {
			runEnd = r.size
		}
		missing = append(missing, span{start: runStart, end: runEnd})
	}
	return missing
}

func (r *remoteFile) installLocked(offset int64, data []byte) {
	// Split the downloaded range into aligned blocks. A coalesced GET of
	// several blocks is stored one block at a time so later random hits work.
	off := offset
	rest := data
	for len(rest) > 0 {
		blockOff := alignBlock(off)
		if blockOff != off {
			// Fetches are always issued at aligned starts.
			return
		}
		size := int64(len(rest))
		if size > remoteBlockSize {
			size = remoteBlockSize
		}
		if _, ok := r.blocks[blockOff]; !ok {
			chunk := make([]byte, size)
			copy(chunk, rest[:size])
			r.blocks[blockOff] = chunk
			r.lru = append(r.lru, blockOff)
			r.cacheBytes += size
			r.fetched += size
		}
		off += size
		rest = rest[size:]
	}
	r.evictLocked()
}

func (r *remoteFile) evictLocked() {
	for r.cacheBytes > remoteCacheLimit && len(r.lru) > 1 {
		oldest := r.lru[0]
		r.lru = r.lru[1:]
		if block, ok := r.blocks[oldest]; ok {
			r.cacheBytes -= int64(len(block))
			delete(r.blocks, oldest)
		}
	}
}

func (r *remoteFile) fetchRange(data []byte, offset int64) error {
	wanted := len(data)
	total := 0
	var lastErr error

	for attempt := 0; attempt < remoteReadAttempts && total < wanted; attempt++ {
		if attempt > 0 {
			select {
			case <-r.ctx.Done():
				return fmt.Errorf("read remote image: %w", r.ctx.Err())
			case <-time.After(remoteRetryBackoff * time.Duration(attempt)):
			}
		}

		start := offset + int64(total)
		end := offset + int64(wanted) - 1

		request, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
		if err != nil {
			return fmt.Errorf("create remote range request: %w", err)
		}
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		response, err := r.client.Do(request)
		if err != nil {
			lastErr = err
			continue
		}

		if response.StatusCode != http.StatusPartialContent {
			response.Body.Close()
			return fmt.Errorf("read remote image range: server returned %s", response.Status)
		}
		actualStart, actualEnd, actualSize, err := parseContentRange(response.Header.Get("Content-Range"))
		if err != nil || actualStart != start || actualEnd != end || actualSize != r.size {
			response.Body.Close()
			if err != nil {
				return fmt.Errorf("read remote image range: %w", err)
			}
			return fmt.Errorf("read remote image range: server returned bytes %d-%d/%d, expected %d-%d/%d", actualStart, actualEnd, actualSize, start, end, r.size)
		}

		count, readErr := io.ReadFull(response.Body, data[total:wanted])
		closeErr := response.Body.Close()
		total += count

		if readErr == nil {
			lastErr = closeErr
			break
		}
		lastErr = readErr
	}

	if total != wanted {
		if lastErr == nil {
			lastErr = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("read remote image at offset %d: %w", offset+int64(total), lastErr)
	}
	return nil
}

func (r *remoteFile) reportProgressLocked() {
	if r.reporter == nil {
		return
	}
	now := time.Now()
	if !r.lastReport.IsZero() && now.Sub(r.lastReport) < progressMinGap {
		return
	}
	r.lastReport = now
	r.reporter(Progress{
		Stage:   "downloading-support-files",
		Message: "Preparing Apple machine identity / downloading support files (first time)",
		Current: r.fetched,
		Total:   0,
		Needed:  spanBytes(r.needed),
	})
}

func (r *remoteFile) FetchedBytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fetched
}

func (r *remoteFile) NeededBytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return spanBytes(r.needed)
}

func addSpan(spans []span, start, end int64) []span {
	if start >= end {
		return spans
	}
	spans = append(spans, span{start: start, end: end})
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end < spans[j].end
		}
		return spans[i].start < spans[j].start
	})
	out := spans[:0]
	for _, s := range spans {
		if len(out) == 0 || s.start > out[len(out)-1].end {
			out = append(out, s)
			continue
		}
		if s.end > out[len(out)-1].end {
			out[len(out)-1].end = s.end
		}
	}
	return out
}

func spanBytes(spans []span) int64 {
	var total int64
	for _, s := range spans {
		total += s.end - s.start
	}
	return total
}
