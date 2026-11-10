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

func TestRemoteFileReadAtCoalescesRangeRequests(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 200_000) // ~5.2 MiB
	var requests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			http.Error(w, "range required", http.StatusBadRequest)
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if start < 0 || end >= int64(len(payload)) || end < start {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	remote := &remoteFile{ctx: ctx, client: server.Client(), url: server.URL, size: int64(len(payload))}

	// Many small APFS-style reads across a 2 MiB window should share one GET.
	buf := make([]byte, 4096)
	for offset := int64(0); offset < 1<<20; offset += 4096 {
		n, err := remote.ReadAt(buf, offset)
		if err != nil {
			t.Fatalf("ReadAt(%d): %v", offset, err)
		}
		if n != len(buf) {
			t.Fatalf("ReadAt(%d): got %d bytes", offset, n)
		}
		if !bytes.Equal(buf, payload[offset:offset+int64(n)]) {
			t.Fatalf("ReadAt(%d): payload mismatch", offset)
		}
	}

	got := requests.Load()
	if got > 4 {
		t.Fatalf("expected coalesced range GETs, got %d requests for 1 MiB of 4 KiB reads", got)
	}

	// A read past the first window should issue at least one more GET, not a busy loop.
	if _, err := remote.ReadAt(buf, 3<<20); err != nil {
		t.Fatal(err)
	}
	if requests.Load() <= got {
		t.Fatal("expected a new range GET after leaving the window")
	}
}

func TestRemoteFileReadAtEOF(t *testing.T) {
	t.Parallel()

	payload := []byte("hello world")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if end >= int64(len(payload)) {
			end = int64(len(payload)) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()

	remote := &remoteFile{ctx: context.Background(), client: server.Client(), url: server.URL, size: int64(len(payload))}
	buf := make([]byte, 32)
	n, err := remote.ReadAt(buf, 0)
	if err != io.EOF {
		t.Fatalf("expected EOF, got n=%d err=%v", n, err)
	}
	if n != len(payload) || !bytes.Equal(buf[:n], payload) {
		t.Fatalf("got %q", buf[:n])
	}
}
