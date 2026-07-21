// Black-box semantic-contract oracle for agri-alert-via-port.
//
// Each TestT* boots a FRESH copy of the arm's compiled binary ($TARGET_BIN,
// exported by score.sh) on a free port, with the seed reloaded — so state
// mutations are isolated per test. Assertions key on CONTRACT.md's observable
// hooks/tokens (data-*, error:* tokens, GeoJSON properties), never on markup,
// so any valid Via realization passes. stdlib only; no Via dependency here.
package hidden

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- process harness -------------------------------------------------------

type app struct {
	base string
	cmd  *exec.Cmd
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

// startApp boots $TARGET_BIN fresh with base env + overrides, waits until it
// answers GET /login, and registers cleanup.
func startApp(t *testing.T, env map[string]string) *app {
	t.Helper()
	bin := os.Getenv("TARGET_BIN")
	if bin == "" {
		t.Fatal("TARGET_BIN not set (run via score.sh)")
	}
	port := freePort(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "PORT="+port)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	a := &app{base: "http://127.0.0.1:" + port, cmd: cmd}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(a.base + "/login")
		if err == nil {
			resp.Body.Close()
			return a
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("binary did not become ready on %s", a.base)
	return nil
}

// newClient returns a client with a cookie jar that does NOT auto-follow
// redirects (so tests can assert 303 + Location while still capturing cookies).
func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// login posts the password form and returns the (now-authenticated) client.
// Tolerant of the exact success status; individual tests assert specifics.
func login(t *testing.T, a *app, password string) *http.Client {
	t.Helper()
	c := newClient(t)
	resp := postForm(t, c, a.base+"/login", map[string]string{"password": password})
	resp.Body.Close()
	return c
}

// ---- HTTP helpers ----------------------------------------------------------

func postForm(t *testing.T, c *http.Client, url string, fields map[string]string) *http.Response {
	t.Helper()
	form := make([]string, 0, len(fields))
	for k, v := range fields {
		form = append(form, urlEncode(k)+"="+urlEncode(v))
	}
	req, err := http.NewRequest("POST", url, strings.NewReader(strings.Join(form, "&")))
	if err != nil {
		t.Fatalf("build POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func do(t *testing.T, c *http.Client, method, url string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func get(t *testing.T, c *http.Client, url string) *http.Response { return do(t, c, "GET", url) }

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func getBody(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp := get(t, c, url)
	return resp.StatusCode, body(t, resp)
}

// cookieFromResponse returns the named cookie parsed from a response's
// Set-Cookie headers, or nil. Unlike a cookie read back out of an
// http.CookieJar (which only carries Name/Value), this preserves the full
// attribute set — HttpOnly, Secure, SameSite, Max-Age — so attribute
// assertions are meaningful.
func cookieFromResponse(resp *http.Response, name string) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// lastListPage fetches the contacts list page holding the highest ids. A newly
// created contact takes the next id and sorts last (PAGE_SIZE 15), so it lands
// on the final page — the page number is derived from the data-count total on
// page 1, not assumed to be page 1.
func lastListPage(t *testing.T, c *http.Client, a *app) string {
	t.Helper()
	_, first := getBody(t, c, a.base+"/")
	const marker = `data-count="`
	i := strings.Index(first, marker)
	if i < 0 {
		t.Fatal("contacts page 1 has no data-count attribute")
	}
	rest := first[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		t.Fatal("malformed data-count attribute")
	}
	n, err := strconv.Atoi(rest[:j])
	if err != nil {
		t.Fatalf("data-count not an integer: %v", err)
	}
	page := 1
	if n > 0 {
		page = (n + 14) / 15
	}
	_, b := getBody(t, c, a.base+"/?page="+strconv.Itoa(page))
	return b
}

func urlEncode(s string) string {
	// minimal application/x-www-form-urlencoded escaping
	var b strings.Builder
	for _, r := range []byte(s) {
		switch {
		case r == ' ':
			b.WriteByte('+')
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '~':
			b.WriteByte(r)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", r))
		}
	}
	return b.String()
}

// ---- assertions ------------------------------------------------------------

func mustContain(t *testing.T, hay, needle, why string) {
	t.Helper()
	if !strings.Contains(hay, needle) {
		t.Fatalf("%s: expected to find %q", why, needle)
	}
}

func mustNotContain(t *testing.T, hay, needle, why string) {
	t.Helper()
	if strings.Contains(hay, needle) {
		t.Fatalf("%s: did NOT expect %q", why, needle)
	}
}

func mustStatus(t *testing.T, got int, want ...int) {
	t.Helper()
	for _, w := range want {
		if got == w {
			return
		}
	}
	t.Fatalf("status %d, want one of %v", got, want)
}

// countSubstr counts non-overlapping occurrences.
func countSubstr(s, sub string) int { return strings.Count(s, sub) }

// ---- GeoJSON ---------------------------------------------------------------

type feature struct {
	Type       string                 `json:"type"`
	Geometry   map[string]any         `json:"geometry"`
	Properties map[string]any         `json:"properties"`
	Extra      map[string]json.RawMessage `json:"-"`
}

type featureColl struct {
	Type     string    `json:"type"`
	Features []feature `json:"features"`
}

func parseGeoJSON(t *testing.T, s string) featureColl {
	t.Helper()
	var fc featureColl
	if err := json.Unmarshal([]byte(s), &fc); err != nil {
		t.Fatalf("parse GeoJSON: %v; body head: %.200s", err, s)
	}
	return fc
}

// ---- SMS mock --------------------------------------------------------------

type smsMock struct {
	srv  *httptest.Server
	mu   sync.Mutex
	msgs []string
}

// newSMSMock returns a mock upstream for AGRI_SMS_URL. If fail is true it
// answers 500 so the port's failure path is exercised.
func newSMSMock(t *testing.T, fail bool) *smsMock {
	t.Helper()
	m := &smsMock{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.msgs = append(m.msgs, string(b))
		m.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *smsMock) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.msgs)
}
