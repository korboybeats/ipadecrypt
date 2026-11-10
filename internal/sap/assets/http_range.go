package assets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	remoteReadAttempts = 3
	// remoteWindowSize is large enough that APFS 4 KiB walks and AEA segment
	// reads coalesce into a handful of HTTP range GETs instead of one-per-page.
	// The previous unbuffered ReadAt issued a fresh Range request for every
	// small read and burned hundreds of wakeups per second on device.
	remoteWindowSize   = 2 << 20
	remoteRetryBackoff = 200 * time.Millisecond
)

type remoteFile struct {
	ctx    context.Context
	client *http.Client
	url    string
	size   int64

	mu         sync.Mutex
	windowOff  int64
	window     []byte
	fetched    int64
	lastReport time.Time
	reporter   ProgressFunc
}

//nolint:wsl // Request construction and validation are clearer in protocol order.
func openRemoteFile(ctx context.Context, client *http.Client) (*remoteFile, error) {
	return openRemoteFileWithProgress(ctx, client, nil)
}

func openRemoteFileWithProgress(ctx context.Context, client *http.Client, reporter ProgressFunc) (*remoteFile, error) {
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

	return &remoteFile{ctx: ctx, client: client, url: macOSRestoreURL, size: size, reporter: reporter}, nil
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

// ReadAt serves small random reads from a sliding window filled with multi-megabyte
// range GETs. Concurrent callers serialize on the window lock; APFS extraction is
// already single-threaded through this ReaderAt.
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

	total := 0
	for total < wanted {
		n, err := r.readAtBuffered(data[total:wanted], offset+int64(total))
		total += n
		if err != nil {
			if errors.Is(err, io.EOF) && total == wanted {
				break
			}
			if total == wanted {
				return total, nil
			}
			return total, err
		}
	}

	if wanted != len(data) {
		return total, io.EOF
	}
	return total, nil
}

func (r *remoteFile) readAtBuffered(data []byte, offset int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(data) == 0 {
		return 0, nil
	}
	if offset < 0 {
		return 0, errors.New("negative remote image offset")
	}
	if offset >= r.size {
		return 0, io.EOF
	}

	if err := r.ensureWindowLocked(offset); err != nil {
		return 0, err
	}

	rel := int(offset - r.windowOff)
	if rel < 0 || rel >= len(r.window) {
		return 0, fmt.Errorf("remote window miss at offset %d", offset)
	}
	n := copy(data, r.window[rel:])
	if n < len(data) && offset+int64(n) >= r.size {
		return n, io.EOF
	}
	return n, nil
}

func (r *remoteFile) ensureWindowLocked(offset int64) error {
	if len(r.window) > 0 && offset >= r.windowOff && offset < r.windowOff+int64(len(r.window)) {
		return nil
	}

	start := offset
	end := offset + remoteWindowSize - 1
	if end >= r.size {
		end = r.size - 1
	}
	if end < start {
		return io.EOF
	}

	buf := make([]byte, end-start+1)
	if err := r.fillRangeLocked(buf, start); err != nil {
		return err
	}
	r.windowOff = start
	r.window = buf
	return nil
}

func (r *remoteFile) fillRangeLocked(data []byte, offset int64) error {
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
		r.fetched += int64(count)
		r.reportProgressLocked()

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
	if !r.lastReport.IsZero() && now.Sub(r.lastReport) < 500*time.Millisecond {
		return
	}
	r.lastReport = now
	r.reporter(Progress{
		Stage:   "downloading-support-files",
		Message: "Preparing Apple machine identity / downloading support files (first time)",
		Current: r.fetched,
		Total:   0,
	})
}

func (r *remoteFile) FetchedBytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fetched
}
