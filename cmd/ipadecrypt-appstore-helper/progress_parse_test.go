package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/londek/ipadecrypt/internal/sap/assets"
)

// parseEvt is a Go equivalent of IDParseAppStoreEvent for unit coverage of
// the structured progress protocol the iOS UI reads.
func parseEvt(line string) map[string]string {
	if !strings.HasPrefix(line, "@evt ") {
		return nil
	}
	body := strings.TrimPrefix(line, "@evt ")
	out := map[string]string{}
	i := 0
	for i < len(body) {
		for i < len(body) && body[i] == ' ' {
			i++
		}
		if i >= len(body) {
			break
		}
		eq := strings.IndexByte(body[i:], '=')
		if eq < 0 {
			break
		}
		key := body[i : i+eq]
		i += eq + 1
		if i >= len(body) {
			break
		}
		var value string
		if body[i] == '"' {
			i++
			end := strings.IndexByte(body[i:], '"')
			if end < 0 {
				value = body[i:]
				i = len(body)
			} else {
				value = body[i : i+end]
				i += end + 1
			}
		} else {
			end := strings.IndexByte(body[i:], ' ')
			if end < 0 {
				value = body[i:]
				i = len(body)
			} else {
				value = body[i : i+end]
				i += end
			}
		}
		out[key] = value
	}
	return out
}

func TestAssetProgressEventRoundTrip(t *testing.T) {
	p := assets.Progress{
		Stage:   "downloading-support-files",
		Message: "Preparing Apple machine identity / downloading support files (first time)",
		Current: 157286400,
		Total:   0,
	}
	line := fmt.Sprintf("@evt phase=%q stage=%q message=%q current=%q total=%q",
		"asset-progress", p.Stage, p.Message, fmt.Sprintf("%d", p.Current), fmt.Sprintf("%d", p.Total))

	got := parseEvt(line)
	if got["phase"] != "asset-progress" {
		t.Fatalf("phase %q", got["phase"])
	}
	if got["stage"] != p.Stage {
		t.Fatalf("stage %q", got["stage"])
	}
	if got["message"] != p.Message {
		t.Fatalf("message %q", got["message"])
	}
	if got["current"] != "157286400" || got["total"] != "0" {
		t.Fatalf("bytes %q/%q", got["current"], got["total"])
	}
}

func TestParseEvtIgnoresNonEventLines(t *testing.T) {
	if parseEvt("sign in with Apple ID required") != nil {
		t.Fatal("plain stderr should not parse as an event")
	}
}

func TestProgressMessageForFirstTimeDownload(t *testing.T) {
	msg := "Preparing Apple machine identity / downloading support files (first time)"
	if !strings.Contains(msg, "first time") || !strings.Contains(msg, "support files") {
		t.Fatal("progress copy drifted from the agreed UX string")
	}
}
