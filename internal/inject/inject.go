package inject

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/derekgould/multi-dev-proxy/internal/proxy"
)

const (
	maxBodySize     = 5 * 1024 * 1024
	widgetScriptTag = `<script src="/__mdp/widget.js"></script>`
)

// headOpenTagRe matches a <head> open tag, with or without attributes, e.g.
// <head> or <head lang="en">. The word boundary after "head" (required next
// char is '>' or whitespace) keeps it from matching <header>.
var headOpenTagRe = regexp.MustCompile(`<head(?:\s[^>]*)?>`)

type Injector struct{}

func New() *Injector { return &Injector{} }

func (inj *Injector) ModifyResponse(resp *http.Response) error {
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "text/html") {
		return nil
	}
	if resp.Body == nil {
		return nil
	}

	resp.Header.Del("Content-Security-Policy")
	resp.Header.Del("Content-Security-Policy-Report-Only")

	encoding := strings.ToLower(resp.Header.Get("Content-Encoding"))
	var reader io.Reader
	switch encoding {
	case "gzip":
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil
		}
		defer gr.Close()
		reader = gr
	case "br":
		reader = brotli.NewReader(resp.Body)
	default:
		reader = resp.Body
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxBodySize+1))
	resp.Body.Close()

	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Transfer-Encoding")

	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
		return nil
	}

	if int64(len(body)) > maxBodySize {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
		return nil
	}

	upstreamName := ""
	if resp.Request != nil {
		upstreamName = proxy.ResolvedUpstream(resp.Request)
	}
	modified := injectWidget(body, upstreamName)
	resp.Body = io.NopCloser(bytes.NewReader(modified))
	resp.ContentLength = int64(len(modified))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(modified)))
	return nil
}

// injectWidget places the widget script tag as early as possible in the
// document — right after the opening <head> tag, ahead of the app's own
// script tags. widget.js registers the routing Service Worker; a classic
// (non-module, non-async/defer) script tag blocks HTML parsing until it
// runs, so placing it first gives SW registration a head start before the
// app's own <script type="module"> starts fetching its import graph.
//
// An inline script setting window.__mdpServedBy is injected right before
// it, naming which upstream server actually produced this response.
// widget.js compares this against the tab's own sessionStorage pin (if any)
// on load, so a stale reload — one that resolved via the shared cookie
// instead of this tab's own choice — can self-correct.
func injectWidget(body []byte, upstreamName string) []byte {
	tag := widgetScriptTag
	if upstreamName != "" {
		nameJSON, _ := json.Marshal(upstreamName)
		tag = "<script>window.__mdpServedBy=" + string(nameJSON) + ";</script>\n" + widgetScriptTag
	}

	s := string(body)
	lower := strings.ToLower(s)

	if loc := headOpenTagRe.FindStringIndex(lower); loc != nil {
		return []byte(s[:loc[1]] + "\n" + tag + s[loc[1]:])
	}
	if idx := strings.Index(lower, "</body>"); idx >= 0 {
		return []byte(s[:idx] + tag + "\n" + s[idx:])
	}
	if idx := strings.Index(lower, "</html>"); idx >= 0 {
		return []byte(s[:idx] + tag + "\n" + s[idx:])
	}
	return append(body, []byte("\n"+tag)...)
}
