package appstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"howett.net/plist"
)

type responseFormat int

const (
	formatXML responseFormat = iota
	formatJSON
)

var (
	documentXMLPattern = regexp.MustCompile(`(?is)<Document\b[^>]*>(.*)</Document>`)
	plistXMLPattern    = regexp.MustCompile(`(?is)<plist\b[^>]*>.*?</plist>`)
	dictXMLPattern     = regexp.MustCompile(`(?is)<dict\b[^>]*>.*</dict>`)
	openStepKVPattern  = regexp.MustCompile(`(?s)^[A-Za-z0-9_.$-]+\s*=`)
)

// send makes an HTTP request, persists cookies, and decodes the response into
// out (when non-nil) per format. The returned *http.Response has an already-
// drained body - callers inspect StatusCode/Header only.
func (c *Client) send(method, url string, headers map[string]string, body []byte, format responseFormat, out any) (*http.Response, error) {
	var r io.Reader
	if len(body) > 0 {
		r = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if err := c.jar.Save(); err != nil {
		return nil, fmt.Errorf("save cookies: %w", err)
	}

	if out == nil {
		io.Copy(io.Discard, res.Body)
		return res, nil
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("apple auth is rate-limited (HTTP 429): %s", responsePreview(data))
	}

	switch format {
	case formatJSON:
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("decode json: %w", err)
		}
	case formatXML:
		if _, err := plist.Unmarshal(normalizePlist(data), out); err != nil {
			return nil, fmt.Errorf("decode plist: %w; response status=%d content-type=%q body=%q",
				err, res.StatusCode, res.Header.Get("Content-Type"), responsePreview(data))
		}
	}

	return res, nil
}

func plistBody(content map[string]any) ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := plist.NewEncoder(buf).Encode(content); err != nil {
		return nil, fmt.Errorf("encode plist: %w", err)
	}

	return buf.Bytes(), nil
}

// normalizePlist unwraps Apple's various plist embeddings. Responses sometimes
// come wrapped in <Document>…</Document>, or are a bare <dict>…</dict> without
// the <plist> envelope, are a bag of <key>/<value> pairs without any outer
// element at all, or are old-style text plist key/value pairs without the
// dictionary braces.
func normalizePlist(body []byte) []byte {
	n := bytes.TrimSpace(body)
	if len(n) == 0 {
		return n
	}

	if m := documentXMLPattern.FindSubmatch(n); len(m) >= 2 {
		if inner := bytes.TrimSpace(m[1]); len(inner) > 0 {
			n = inner
		}
	}

	if m := plistXMLPattern.Find(n); len(m) > 0 {
		n = bytes.TrimSpace(m)
	}

	if m := dictXMLPattern.Find(n); len(m) > 0 {
		return bytes.TrimSpace(m)
	}

	if bytes.Contains(n, []byte("<key>")) {
		return []byte("<dict>" + string(n) + "</dict>")
	}

	if openStepKVPattern.Match(n) && bytes.Contains(n, []byte(";")) {
		return []byte("{\n" + string(n) + "\n}")
	}

	return n
}

func responsePreview(body []byte) string {
	const limit = 240

	s := strings.TrimSpace(string(body))
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t':
			return ' '
		default:
			return r
		}
	}, s)
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = redactResponsePreview(s)
	if len(s) > limit {
		s = s[:limit] + "..."
	}
	return s
}

func redactResponsePreview(s string) string {
	for _, key := range []string{
		"passwordToken",
		"dsPersonId",
		"m-allowed",
		"mMeToken",
		"accountInfo",
	} {
		s = redactAfterKey(s, key)
	}
	return s
}

func redactAfterKey(s, key string) string {
	if i := strings.Index(s, key); i >= 0 {
		cut := strings.Index(s[i:], ";")
		if cut < 0 {
			return s[:i+len(key)] + "=<redacted>"
		}
		return s[:i+len(key)] + "=<redacted>" + s[i+cut:]
	}
	return s
}
