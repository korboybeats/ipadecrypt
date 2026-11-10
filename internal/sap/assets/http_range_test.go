package assets

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseContentRange(t *testing.T) {
	t.Parallel()

	start, end, size, err := parseContentRange("bytes 0-0/12345")
	if err != nil {
		t.Fatal(err)
	}
	if start != 0 || end != 0 || size != 12345 {
		t.Fatalf("got %d-%d/%d", start, end, size)
	}

	if _, _, _, err := parseContentRange("bytes 0-0/*"); err == nil {
		t.Fatal("expected error for unknown size")
	}
	if _, _, _, err := parseContentRange("invalid"); err == nil {
		t.Fatal("expected error for invalid header")
	}
}

func rangeServer(t *testing.T, payload []byte, requests *atomic.Int64, bytesServed *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if start < 0 || end >= int64(len(payload)) || end < start {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		n, _ := w.Write(payload[start : end+1])
		bytesServed.Add(int64(n))
	}))
}

func newTestRemote(t *testing.T, payload []byte) (*remoteFile, *atomic.Int64, *atomic.Int64, *httptest.Server) {
	t.Helper()
	var requests, served atomic.Int64
	server := rangeServer(t, payload, &requests, &served)
	t.Cleanup(server.Close)
	remote := &remoteFile{
		ctx:    context.Background(),
		client: server.Client(),
		url:    server.URL,
		size:   int64(len(payload)),
		blocks: make(map[int64][]byte),
	}
	return remote, &requests, &served, server
}

func TestRemoteFileSmallScatteredReadsDoNotPullHugeWindows(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 800_000) // ~20 MiB
	remote, requests, served, _ := newTestRemote(t, payload)

	buf := make([]byte, 4096)
	offsets := []int64{0, 10 << 20, 18 << 20}
	for _, off := range offsets {
		n, err := remote.ReadAt(buf, off)
		if err != nil {
			t.Fatalf("ReadAt(%d): %v", off, err)
		}
		if n != len(buf) || !bytes.Equal(buf, payload[off:off+int64(n)]) {
			t.Fatalf("ReadAt(%d): payload mismatch", off)
		}
	}

	fetched := remote.FetchedBytes()
	needed := remote.NeededBytes()
	if needed != int64(len(buf)*len(offsets)) {
		t.Fatalf("needed = %d, want %d", needed, len(buf)*len(offsets))
	}
	// Three far-apart 4 KiB reads must not each drag in a 2 MiB window.
	maxFetched := int64(len(offsets)) * remoteBlockSize * 2
	if fetched > maxFetched {
		t.Fatalf("fetched %d bytes for 3 scattered 4 KiB reads; want <= %d", fetched, maxFetched)
	}
	if served.Load() != fetched {
		t.Fatalf("server sent %d, cache recorded %d", served.Load(), fetched)
	}
	if requests.Load() > 6 {
		t.Fatalf("too many range GETs for scattered reads: %d", requests.Load())
	}
}

func TestRemoteFileDoesNotRefetchCachedBlocks(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("x"), 2<<20)
	remote, requests, _, _ := newTestRemote(t, payload)

	buf := make([]byte, 8192)
	if _, err := remote.ReadAt(buf, 4096); err != nil {
		t.Fatal(err)
	}
	first := requests.Load()
	fetched := remote.FetchedBytes()
	if first == 0 {
		t.Fatal("expected an initial range GET")
	}
	if _, err := remote.ReadAt(buf, 4096); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ReadAt(buf, 0); err != nil { // already cached in the same block
		t.Fatal(err)
	}
	if requests.Load() != first {
		t.Fatalf("re-read issued %d more GETs", requests.Load()-first)
	}
	if remote.FetchedBytes() != fetched {
		t.Fatalf("cached re-read changed fetched bytes %d -> %d", fetched, remote.FetchedBytes())
	}
}

func TestRemoteFileSequentialReadsCoalesceAndReadAhead(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 200_000) // ~5.2 MiB
	remote, requests, _, _ := newTestRemote(t, payload)

	buf := make([]byte, 4096)
	for offset := int64(0); offset < 1<<20; offset += 4096 {
		n, err := remote.ReadAt(buf, offset)
		if err != nil {
			t.Fatalf("ReadAt(%d): %v", offset, err)
		}
		if n != len(buf) || !bytes.Equal(buf, payload[offset:offset+int64(n)]) {
			t.Fatalf("ReadAt(%d): payload mismatch", offset)
		}
	}

	got := requests.Load()
	if got > 8 {
		t.Fatalf("expected coalesced sequential GETs, got %d requests for 1 MiB of 4 KiB reads", got)
	}
	// Needed should be exactly the 1 MiB touched; fetched may include read-ahead.
	if remote.NeededBytes() != 1<<20 {
		t.Fatalf("needed = %d, want %d", remote.NeededBytes(), 1<<20)
	}
	if remote.FetchedBytes() < remote.NeededBytes() {
		t.Fatalf("fetched %d < needed %d", remote.FetchedBytes(), remote.NeededBytes())
	}
}

func TestRemoteFileReadAtEOF(t *testing.T) {
	t.Parallel()

	payload := []byte("hello world")
	var requests, served atomic.Int64
	server := rangeServer(t, payload, &requests, &served)
	defer server.Close()
	remote := &remoteFile{
		ctx:    context.Background(),
		client: server.Client(),
		url:    server.URL,
		size:   int64(len(payload)),
		blocks: make(map[int64][]byte),
	}
	buf := make([]byte, 32)
	n, err := remote.ReadAt(buf, 0)
	if err != io.EOF {
		t.Fatalf("expected EOF, got n=%d err=%v", n, err)
	}
	if n != len(payload) || !bytes.Equal(buf[:n], payload) {
		t.Fatalf("got %q", buf[:n])
	}
}

func TestAddSpanMergesAdjacentAndOverlapping(t *testing.T) {
	t.Parallel()

	spans := addSpan(nil, 0, 100)
	spans = addSpan(spans, 100, 150)
	spans = addSpan(spans, 40, 80)
	spans = addSpan(spans, 200, 250)
	if spanBytes(spans) != 200 {
		t.Fatalf("spanBytes=%d, want 200 (0-150 + 200-250)", spanBytes(spans))
	}
	if len(spans) != 2 {
		t.Fatalf("merged into %d spans, want 2", len(spans))
	}
}

func TestProgressThrottleIsAboutFourPerSecond(t *testing.T) {
	t.Parallel()

	var n int
	remote := &remoteFile{
		blocks:   make(map[int64][]byte),
		reporter: func(Progress) { n++ },
	}
	start := time.Now()
	for i := 0; i < 20; i++ {
		remote.mu.Lock()
		remote.reportProgressLocked()
		remote.mu.Unlock()
	}
	elapsed := time.Since(start)
	if n > 1 && elapsed < progressMinGap {
		t.Fatalf("emitted %d events in %s, expected throttle", n, elapsed)
	}
	if n != 1 {
		t.Fatalf("20 immediate reports produced %d events, want 1", n)
	}
}
