package inject

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
)

const (
	maxBodySize     = 5 * 1024 * 1024
	widgetScriptTag = `<script src="/__mdp/widget.js"></script>`
)

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

	modified := injectWidget(body)
	resp.Body = io.NopCloser(bytes.NewReader(modified))
	resp.ContentLength = int64(len(modified))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(modified)))
	return nil
}

func injectWidget(body []byte) []byte {
	s := string(body)
	lower := strings.ToLower(s)

	if idx := strings.Index(lower, "</body>"); idx >= 0 {
		return []byte(s[:idx] + widgetScriptTag + "\n" + s[idx:])
	}
	if idx := strings.Index(lower, "</html>"); idx >= 0 {
		return []byte(s[:idx] + widgetScriptTag + "\n" + s[idx:])
	}
	return append(body, []byte("\n"+widgetScriptTag)...)
}
