package pipeline

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupIPA(t *testing.T) {
	tests := []struct {
		name         string
		entries      []string
		opts         CleanupOptions
		wantMetadata bool
		wantWatch    int
		wantEntries  []string
	}{
		{
			name:         "metadata only",
			entries:      []string{"Payload/App.app/App", "iTunesMetadata.plist"},
			opts:         CleanupOptions{StripMetadata: true},
			wantMetadata: true,
			wantEntries:  []string{"Payload/App.app/App"},
		},
		{
			name:        "watch only",
			entries:     []string{"Payload/App.app/App", "Payload/App.app/Watch/Watch.app/Watch"},
			opts:        CleanupOptions{StripWatch: true},
			wantWatch:   1,
			wantEntries: []string{"Payload/App.app/App"},
		},
		{
			name:         "metadata and watch",
			entries:      []string{"Payload/App.app/App", "Payload/App.app/Watch/Watch.app/Watch", "iTunesMetadata.plist"},
			opts:         CleanupOptions{StripMetadata: true, StripWatch: true},
			wantMetadata: true,
			wantWatch:    1,
			wantEntries:  []string{"Payload/App.app/App"},
		},
		{
			name:        "flags disabled",
			entries:     []string{"Payload/App.app/App", "Payload/App.app/Watch/Watch.app/Watch", "iTunesMetadata.plist"},
			opts:        CleanupOptions{},
			wantEntries: []string{"Payload/App.app/App", "Payload/App.app/Watch/Watch.app/Watch", "iTunesMetadata.plist"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipaPath := filepath.Join(t.TempDir(), "test.ipa")
			writeTestIPA(t, ipaPath, tt.entries)

			got, err := CleanupIPA(ipaPath, tt.opts)
			if err != nil {
				t.Fatalf("CleanupIPA: %v", err)
			}
			if got.MetadataRemoved != tt.wantMetadata || got.WatchRemoved != tt.wantWatch {
				t.Fatalf("CleanupIPA = %+v, want metadata=%v watch=%d", got, tt.wantMetadata, tt.wantWatch)
			}

			assertZipEntries(t, ipaPath, tt.wantEntries)
		})
	}
}

func TestCleanupIPANoopDoesNotRewrite(t *testing.T) {
	ipaPath := filepath.Join(t.TempDir(), "test.ipa")
	writeTestIPA(t, ipaPath, []string{"Payload/App.app/App"})

	oldTime := time.Now().Add(-time.Hour).Round(time.Second)
	if err := os.Chtimes(ipaPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	got, err := CleanupIPA(ipaPath, CleanupOptions{StripMetadata: true, StripWatch: true})
	if err != nil {
		t.Fatalf("CleanupIPA: %v", err)
	}
	if got.MetadataRemoved || got.WatchRemoved != 0 {
		t.Fatalf("CleanupIPA = %+v, want no removals", got)
	}

	st, err := os.Stat(ipaPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !st.ModTime().Equal(oldTime) {
		t.Fatalf("noop cleanup rewrote IPA: modtime=%s want %s", st.ModTime(), oldTime)
	}
}

func writeTestIPA(t *testing.T, path string, entries []string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create ipa: %v", err)
	}

	zw := zip.NewWriter(f)
	for _, name := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(name)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close ipa: %v", err)
	}
}

func assertZipEntries(t *testing.T, path string, want []string) {
	t.Helper()

	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open ipa: %v", err)
	}
	defer zr.Close()

	got := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		got[f.Name] = true
	}
	if len(got) != len(want) {
		t.Fatalf("entries count = %d, want %d (%v)", len(got), len(want), got)
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("missing entry %s in %v", name, got)
		}
	}
}
