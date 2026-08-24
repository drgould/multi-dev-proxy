package inject

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/derekgould/multi-dev-proxy/internal/proxy"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

func makeResp(body string, contentType string, extraHeaders map[string]string) *http.Response {
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	resp.Header.Set("Content-Type", contentType)
	for k, v := range extraHeaders {
		resp.Header.Set(k, v)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestInjectPlainHTML(t *testing.T) {
	inj := New()
	resp := makeResp("<html><body><h1>App</h1></body></html>", "text/html", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, widgetScriptTag) {
		t.Error("widget script tag not injected")
	}
	if !strings.Contains(body, widgetScriptTag+"\n</body>") {
		t.Errorf("script not before </body>, got: %q", body)
	}
	cl := resp.Header.Get("Content-Length")
	if cl != fmt.Sprintf("%d", len(body)) {
		t.Errorf("Content-Length %q != body length %d", cl, len(body))
	}
}

func TestInjectGzipped(t *testing.T) {
	htmlBody := "<html><body><p>Hello</p></body></html>"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte(htmlBody))
	gz.Close()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	resp.Header.Set("Content-Type", "text/html")
	resp.Header.Set("Content-Encoding", "gzip")

	inj := New()
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Error("Content-Encoding should be removed after decompression")
	}
	body := readBody(t, resp)
	if !strings.Contains(body, widgetScriptTag) {
		t.Error("widget not injected into gzipped response")
	}
}

func TestSkipNonHTML(t *testing.T) {
	inj := New()
	original := `{"key":"value"}`
	resp := makeResp(original, "application/json", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if strings.Contains(body, widgetScriptTag) {
		t.Error("widget injected into non-HTML response")
	}
}

func TestSkipNilBody(t *testing.T) {
	inj := New()
	resp := &http.Response{
		StatusCode: 204,
		Header:     make(http.Header),
		Body:       nil,
	}
	resp.Header.Set("Content-Type", "text/html")
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatalf("unexpected error on nil body: %v", err)
	}
}

func TestInjectNoBodyTag(t *testing.T) {
	inj := New()
	resp := makeResp("<p>Fragment</p>", "text/html", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, widgetScriptTag) {
		t.Error("widget not appended when no </body> or </html>")
	}
}

func TestInjectServedByMarker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head></head><body></body></html>`))
	}))
	defer upstream.Close()

	reg := registry.New()
	if err := reg.Register(&registry.ServerEntry{
		Name: "app/main",
		Repo: "app",
		Port: upstream.Listener.Addr().(*net.TCPAddr).Port,
		PID:  1,
	}); err != nil {
		t.Fatal(err)
	}
	p := proxy.NewProxy(reg, 3000, "")
	p.SetModifyResponse(New().ModifyResponse)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, `window.__mdpServedBy="app/main";`) {
		t.Errorf("response missing servedBy marker, got: %q", body)
	}
	if strings.Index(body, "__mdpServedBy") > strings.Index(body, widgetScriptTag) {
		t.Error("servedBy marker must come before the widget script tag")
	}
}

func TestInjectRightAfterHeadTag(t *testing.T) {
	inj := New()
	resp := makeResp(`<html><head><script type="module" src="/src/main.tsx"></script></head><body></body></html>`, "text/html", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "<head>\n"+widgetScriptTag) {
		t.Errorf("script not injected right after <head>, got: %q", body)
	}
	if strings.Index(body, widgetScriptTag) > strings.Index(body, `src="/src/main.tsx"`) {
		t.Error("widget script must come before the app's own script tag so it registers first")
	}
}

func TestInjectDoesNotMatchHeaderElement(t *testing.T) {
	inj := New()
	resp := makeResp(`<html><head lang="en"><title>App</title></head><body><header>site header</header></body></html>`, "text/html", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `<head lang="en">`+"\n"+widgetScriptTag) {
		t.Errorf("script not injected right after <head lang=\"en\">, got: %q", body)
	}
	if strings.Contains(body, "<header>"+widgetScriptTag) || strings.Contains(body, widgetScriptTag+"site header") {
		t.Error("widget script must not be injected into the <header> element")
	}
}

func TestInjectAfterHeadTagWithAttrs(t *testing.T) {
	inj := New()
	resp := makeResp(`<html><head lang="en"><title>App</title></head><body></body></html>`, "text/html", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `<head lang="en">`+"\n"+widgetScriptTag) {
		t.Errorf("script not injected right after <head lang=\"en\">, got: %q", body)
	}
}

func TestInjectBeforeHtmlTag(t *testing.T) {
	inj := New()
	resp := makeResp("<html><p>No body tag</p></html>", "text/html", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, widgetScriptTag+"\n</html>") {
		t.Errorf("script not before </html>, got: %q", body)
	}
}

func TestCSPStripped(t *testing.T) {
	inj := New()
	resp := makeResp("<html><body></body></html>", "text/html", map[string]string{
		"Content-Security-Policy": "default-src 'self'",
	})
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Security-Policy") != "" {
		t.Error("CSP header should be stripped")
	}
}

func TestContentLengthUpdated(t *testing.T) {
	inj := New()
	resp := makeResp("<html><body><p>Hi</p></body></html>", "text/html", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	expected := fmt.Sprintf("%d", len(body))
	if resp.Header.Get("Content-Length") != expected {
		t.Errorf("Content-Length %q != %q", resp.Header.Get("Content-Length"), expected)
	}
}

func TestInjectBrotli(t *testing.T) {
	htmlBody := "<html><body><p>Brotli compressed</p></body></html>"
	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	bw.Write([]byte(htmlBody))
	bw.Close()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	resp.Header.Set("Content-Type", "text/html")
	resp.Header.Set("Content-Encoding", "br")

	inj := New()
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Error("Content-Encoding should be removed after brotli decompression")
	}
	body := readBody(t, resp)
	if !strings.Contains(body, widgetScriptTag) {
		t.Error("widget not injected into brotli-compressed response")
	}
	if !strings.Contains(body, "Brotli compressed") {
		t.Error("original content lost after brotli decompression")
	}
}

func TestInjectOversizedBody(t *testing.T) {
	large := "<html><body>" + strings.Repeat("x", maxBodySize) + "</body></html>"
	inj := New()
	resp := makeResp(large, "text/html", nil)
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if strings.Contains(body, widgetScriptTag) {
		t.Error("widget should not be injected into oversized response")
	}
	cl := resp.Header.Get("Content-Length")
	if cl != fmt.Sprintf("%d", len(body)) {
		t.Errorf("Content-Length %q != body length %d", cl, len(body))
	}
}

func TestTransferEncodingStripped(t *testing.T) {
	inj := New()
	resp := makeResp("<html><body></body></html>", "text/html", map[string]string{
		"Transfer-Encoding": "chunked",
	})
	if err := inj.ModifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Transfer-Encoding") != "" {
		t.Error("Transfer-Encoding should be stripped")
	}
}
