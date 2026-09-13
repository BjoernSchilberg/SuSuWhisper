package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// setupTest points all persistent storage into a temporary directory so
// tests never touch the real data/ and uploads/ directories.
func setupTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldFilePath, oldUploadDir := filePath, uploadDir
	filePath = filepath.Join(dir, "articles.json")
	uploadDir = filepath.Join(dir, "uploads")
	mutex.Lock()
	articles = make(map[string]Article)
	mutex.Unlock()
	t.Cleanup(func() {
		filePath, uploadDir = oldFilePath, oldUploadDir
	})
	return dir
}

// pngData starts with the PNG signature, which is enough for content sniffing.
var pngData = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func upload(t *testing.T, id, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/upload?id="+url.QueryEscape(id), &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	uploadHandler(rec, req)
	return rec
}

func addArticle(id, title, content string) {
	mutex.Lock()
	articles[id] = Article{ID: id, Title: title, Content: content, CreatedAt: time.Now()}
	mutex.Unlock()
}

func getArticle(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/article?id="+id, nil)
	rec := httptest.NewRecorder()
	getArticleHandler(rec, req)
	return rec
}

func postArticle(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	createArticleHandler(rec, req)
	return rec
}

// --- 1. Stored XSS -----------------------------------------------------------

func TestArticleRemovesScripts(t *testing.T) {
	setupTest(t)
	addArticle("abcd1234", "XSS", `<p>Hallo</p><script>alert(1)</script>`+
		`<img src="x" onerror="alert(2)"><a href="javascript:alert(3)">link</a>`)

	body := getArticle(t, "abcd1234").Body.String()

	if !strings.Contains(body, "<p>Hallo</p>") {
		t.Errorf("harmless content missing:\n%s", body)
	}
	for _, bad := range []string{"<script", "alert(1)", "onerror", "javascript:"} {
		if strings.Contains(body, bad) {
			t.Errorf("article page contains %q:\n%s", bad, body)
		}
	}
}

func TestArticleKeepsEditorFormatting(t *testing.T) {
	setupTest(t)
	addArticle("abcd1234", "Format", `<p style="text-align: center;">zentriert</p>`+
		`<p><span style="background-color: #fbeeb8;">markiert</span></p>`+
		`<ol><li><strong>fett</strong> und <em>kursiv</em></li></ol>`+
		`<img src="uploads/abcd1234/bild.png" alt="Bild" width="300" height="200">`)

	body := getArticle(t, "abcd1234").Body.String()

	for _, want := range []string{
		"text-align: center", "background-color: #fbeeb8",
		"<strong>fett</strong>", "<em>kursiv</em>", "<ol><li>",
		`src="uploads/abcd1234/bild.png"`, `alt="Bild"`, `width="300"`, `height="200"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("article page lost %q:\n%s", want, body)
		}
	}
}

func TestArticlePageForbidsScripts(t *testing.T) {
	setupTest(t)
	addArticle("abcd1234", "CSP", "<p>x</p>")

	csp := getArticle(t, "abcd1234").Header().Get("Content-Security-Policy")

	if !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("Content-Security-Policy = %q, want script-src 'none'", csp)
	}
}

func TestCreateArticleStoresSanitizedContent(t *testing.T) {
	setupTest(t)

	postArticle(t, url.Values{
		"id":      {"abcd1234"},
		"title":   {"Neu"},
		"content": {`<p>ok</p><script>alert(1)</script>`},
	})

	mutex.Lock()
	stored := articles["abcd1234"].Content
	mutex.Unlock()
	if strings.Contains(stored, "<script") || !strings.Contains(stored, "<p>ok</p>") {
		t.Errorf("stored content = %q, want sanitized", stored)
	}
}

// --- 2. Path traversal -------------------------------------------------------

func TestUploadRejectsPathTraversal(t *testing.T) {
	dir := setupTest(t)

	rec := upload(t, "../escape", "articles.json", pngData)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Errorf("upload escaped the uploads directory")
	}
}

func TestUploadRejectsMalformedIDs(t *testing.T) {
	setupTest(t)
	for _, id := range []string{"", "abc", "abcd123!", "abcd12345", "..", "abcd/234"} {
		if rec := upload(t, id, "bild.png", pngData); rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: status = %d, want %d", id, rec.Code, http.StatusBadRequest)
		}
	}
}

func TestUploadIgnoresClientFileName(t *testing.T) {
	setupTest(t)

	rec := upload(t, "abcd1234", "article.html.png", pngData)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var resp struct{ Location string }
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^/uploads/abcd1234/[0-9a-f]{16}\.png$`).MatchString(resp.Location) {
		t.Errorf("location = %q, want server-generated file name", resp.Location)
	}
	name := filepath.Base(resp.Location)
	if _, err := os.Stat(filepath.Join(uploadDir, "abcd1234", name)); err != nil {
		t.Errorf("uploaded file not stored: %v", err)
	}
}

func TestCreateRejectsMalformedID(t *testing.T) {
	setupTest(t)

	rec := postArticle(t, url.Values{"id": {"../x"}, "title": {"t"}, "content": {"c"}})

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(articles) != 0 {
		t.Errorf("article with malformed ID was stored")
	}
}

func TestGeneratedIDsAreValid(t *testing.T) {
	for i := 0; i < 100; i++ {
		if id := generateID(); !validID(id) {
			t.Fatalf("generateID() = %q is not a valid ID", id)
		}
	}
}

// --- 3. Overwriting other people's articles ---------------------------------

func TestCreateRejectsExistingID(t *testing.T) {
	setupTest(t)
	addArticle("abcd1234", "Original", "<p>alt</p>")

	rec := postArticle(t, url.Values{"id": {"abcd1234"}, "title": {"Fremd"}, "content": {"<p>neu</p>"}})

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if got := articles["abcd1234"].Title; got != "Original" {
		t.Errorf("title = %q, existing article was overwritten", got)
	}
}

func TestUploadRejectsPublishedArticle(t *testing.T) {
	setupTest(t)
	addArticle("abcd1234", "Original", "<p>alt</p>")

	rec := upload(t, "abcd1234", "bild.png", pngData)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

// --- 4. Limits ---------------------------------------------------------------

// override sets *p to v for the duration of the test.
func override[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

func TestUploadRejectsNonImages(t *testing.T) {
	setupTest(t)
	files := map[string]string{
		"html.png": "<html><script>alert(1)</script></html>",
		"bild.svg": `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		"text.jpg": "nur text",
	}
	for name, data := range files {
		if rec := upload(t, "abcd1234", name, []byte(data)); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, http.StatusUnsupportedMediaType)
		}
	}
}

func TestUploadExtensionFollowsContent(t *testing.T) {
	setupTest(t)

	rec := upload(t, "abcd1234", "bild.html", pngData)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `.png"`) {
		t.Errorf("response = %s, want .png extension", rec.Body)
	}
}

func TestUploadRejectsTooLargeFile(t *testing.T) {
	setupTest(t)
	override(t, &maxUploadBytes, 1024)
	big := append(append([]byte{}, pngData...), make([]byte, 2048)...)

	if rec := upload(t, "abcd1234", "gross.png", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestCreateRejectsTooLargeArticle(t *testing.T) {
	setupTest(t)
	override(t, &maxArticleBytes, 1024)

	rec := postArticle(t, url.Values{
		"id": {"abcd1234"}, "title": {"t"}, "content": {strings.Repeat("x", 2048)},
	})

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(articles) != 0 {
		t.Errorf("oversized article was stored")
	}
}

func TestUploadRejectedWhenStorageFull(t *testing.T) {
	setupTest(t)
	override(t, &storageQuota, 100)
	os.MkdirAll(filepath.Join(uploadDir, "other123"), 0755)
	os.WriteFile(filepath.Join(uploadDir, "other123", "a.png"), make([]byte, 50), 0644)

	if rec := upload(t, "abcd1234", "bild.png", pngData); rec.Code != http.StatusInsufficientStorage {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInsufficientStorage)
	}
}

func TestCreateRejectedWhenStorageFull(t *testing.T) {
	setupTest(t)
	override(t, &storageQuota, 10)

	rec := postArticle(t, url.Values{"id": {"abcd1234"}, "title": {"t"}, "content": {"<p>mehr als zehn Bytes</p>"}})

	if rec.Code != http.StatusInsufficientStorage {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInsufficientStorage)
	}
}

func postFrom(h http.Handler, remoteAddr string) int {
	req := httptest.NewRequest(http.MethodPost, "/upload?id=invalid!", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestRateLimitPerClient(t *testing.T) {
	setupTest(t)
	override(t, &rateLimit, 2)
	h := routes()

	for i := 0; i < 2; i++ {
		if code := postFrom(h, "192.0.2.1:1234"); code == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate limited too early", i+1)
		}
	}
	if code := postFrom(h, "192.0.2.1:1234"); code != http.StatusTooManyRequests {
		t.Errorf("third request: status = %d, want %d", code, http.StatusTooManyRequests)
	}
	if code := postFrom(h, "192.0.2.2:1234"); code == http.StatusTooManyRequests {
		t.Errorf("other client was rate limited")
	}
}

func TestRateLimitIgnoresReading(t *testing.T) {
	setupTest(t)
	override(t, &rateLimit, 1)
	h := routes()

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/overview", nil))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("GET request %d was rate limited", i+1)
		}
	}
}

func TestClientIPTrustsProxyHeaderOnlyWhenConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 198.51.100.7")

	override(t, &trustProxy, false)
	if got := clientIP(req); got != "127.0.0.1" {
		t.Errorf("without trustProxy: clientIP = %q, want 127.0.0.1", got)
	}
	override(t, &trustProxy, true)
	if got := clientIP(req); got != "198.51.100.7" {
		t.Errorf("with trustProxy: clientIP = %q, want 198.51.100.7 (added by the proxy)", got)
	}
}

func TestUploadsServedWithoutSniffing(t *testing.T) {
	setupTest(t)
	os.MkdirAll(filepath.Join(uploadDir, "abcd1234"), 0755)
	os.WriteFile(filepath.Join(uploadDir, "abcd1234", "alt.html"), []byte("<script>alert(1)</script>"), 0644)

	rec := httptest.NewRecorder()
	routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/uploads/abcd1234/alt.html", nil))

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want sandbox", got)
	}
}

// --- 5. Listen address and configuration ------------------------------------

// keepLimits restores all limits after tests that call loadConfig.
func keepLimits(t *testing.T) {
	override(t, &storageQuota, storageQuota)
	override(t, &rateLimit, rateLimit)
	override(t, &trustProxy, trustProxy)
}

func TestListenAddrDefaultsToLocalhost(t *testing.T) {
	keepLimits(t)
	for _, name := range []string{"SUSU_ADDR", "SUSU_STORAGE_QUOTA_MB", "SUSU_RATE_LIMIT", "SUSU_TRUST_PROXY"} {
		t.Setenv(name, "")
	}

	addr, err := loadConfig()

	if err != nil {
		t.Fatal(err)
	}
	if addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q, want 127.0.0.1:8080", addr)
	}
}

func TestConfigFromEnvironment(t *testing.T) {
	keepLimits(t)
	t.Setenv("SUSU_ADDR", ":9090")
	t.Setenv("SUSU_STORAGE_QUOTA_MB", "5")
	t.Setenv("SUSU_RATE_LIMIT", "7")
	t.Setenv("SUSU_TRUST_PROXY", "true")

	addr, err := loadConfig()

	if err != nil {
		t.Fatal(err)
	}
	if addr != ":9090" || storageQuota != 5<<20 || rateLimit != 7 || !trustProxy {
		t.Errorf("got addr=%q quota=%d rate=%d trustProxy=%v", addr, storageQuota, rateLimit, trustProxy)
	}
}

func TestConfigRejectsInvalidValues(t *testing.T) {
	for name, value := range map[string]string{
		"SUSU_STORAGE_QUOTA_MB": "viel",
		"SUSU_RATE_LIMIT":       "-1",
		"SUSU_TRUST_PROXY":      "vielleicht",
	} {
		t.Run(name, func(t *testing.T) {
			keepLimits(t)
			t.Setenv(name, value)
			if _, err := loadConfig(); err == nil {
				t.Errorf("%s=%q: want error", name, value)
			}
		})
	}
}
