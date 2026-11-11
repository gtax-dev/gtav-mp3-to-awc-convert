package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAPI records the last request and answers like the real /convert endpoint.
func fakeAPI(t *testing.T, status int, body string, headers map[string]string) (*httptest.Server, *http.Request, map[string]string) {
	t.Helper()
	var got http.Request
	fields := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		if err := r.ParseMultipartForm(16 << 20); err == nil {
			for k, v := range r.MultipartForm.Value {
				fields[k] = v[0]
			}
			if fh := r.MultipartForm.File["mp3"]; len(fh) == 1 {
				fields["mp3:filename"] = fh[0].Filename
			}
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got, fields
}

func tempMP3(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, bytes.Repeat([]byte{0xff}, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runIn(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestConvertFivemZip(t *testing.T) {
	dir := t.TempDir()
	in := tempMP3(t, dir, "Air Horn.mp3", 100)
	srv, req, fields := fakeAPI(t, 200, "PKzip", map[string]string{"X-Result-Name": "airhorn_sfx.zip", "X-RateLimit-Remaining": "9"})

	code, out, errOut := runIn(t, dir, "-api-key", "k1", "-api-url", srv.URL, "-start", "1.5", "-length", "3", in)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if req.Header.Get("Authorization") != "Bearer k1" || req.URL.Path != "/convert" {
		t.Errorf("request: auth=%q path=%q", req.Header.Get("Authorization"), req.URL.Path)
	}
	want := map[string]string{"format": "fivem-zip", "projectName": "Air Horn", "trimStartSec": "1.5", "trimLengthSec": "3", "mp3:filename": "Air Horn.mp3"}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("field %s = %q, want %q", k, fields[k], v)
		}
	}
	if _, ok := fields["bankName"]; ok {
		t.Error("bankName sent although not set")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "airhorn_sfx.zip")); err != nil || string(data) != "PKzip" {
		t.Fatalf("output: %q %v", data, err)
	}
	if !strings.Contains(out, "wrote airhorn_sfx.zip") || !strings.Contains(errOut, "conversions left today: 9") {
		t.Errorf("stdout=%q stderr=%q", out, errOut)
	}
}

func TestConvertDlcRpfGoesIntoProjectFolder(t *testing.T) {
	dir := t.TempDir()
	in := tempMP3(t, dir, "x.mp3", 10)
	srv, _, fields := fakeAPI(t, 200, "RPF7", map[string]string{"X-Result-Name": "dlc.rpf"})

	code, _, errOut := runIn(t, dir, "-api-key", "k", "-api-url", srv.URL, "-format", "dlc-rpf", "-project", "My Horn!", "-sound", "honk", in)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if fields["format"] != "dlc-rpf" || fields["streamName"] != "honk" {
		t.Errorf("fields = %v", fields)
	}
	if _, err := os.Stat(filepath.Join(dir, "myhorn", "dlc.rpf")); err != nil {
		t.Fatalf("expected myhorn/dlc.rpf: %v", err)
	}
}

func TestServerNameCannotEscapeWorkingDir(t *testing.T) {
	if got := outputPath(options{format: "fivem-zip"}, "p", "../../evil.zip"); got != "evil.zip" {
		t.Errorf("outputPath = %q", got)
	}
	if got := outputPath(options{format: "fivem-zip"}, "p", ""); got != "p_sfx.zip" {
		t.Errorf("fallback = %q", got)
	}
}

func TestAPIErrorIsReported(t *testing.T) {
	dir := t.TempDir()
	in := tempMP3(t, dir, "a.mp3", 10)
	srv, _, _ := fakeAPI(t, 429, `{"error":"Daily MP3 to AWC limit reached (10 per day)."}`, map[string]string{"Retry-After": "3600"})

	code, _, errOut := runIn(t, dir, "-api-key", "k", "-api-url", srv.URL, in)
	if code != 1 || !strings.Contains(errOut, "Daily MP3 to AWC limit reached") || !strings.Contains(errOut, "retry after 3600s") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "a_sfx.zip")); err == nil {
		t.Error("wrote an output file for a failed request")
	}
}

func TestUsageErrorsNeverCallTheAPI(t *testing.T) {
	dir := t.TempDir()
	small := tempMP3(t, dir, "a.mp3", 10)
	other := tempMP3(t, dir, "b.mp3", 10)
	big := tempMP3(t, dir, "big.mp3", maxUploadBytes+1)
	wav := filepath.Join(dir, "c.wav")
	_ = os.WriteFile(wav, []byte("x"), 0o644)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("API was called")
	}))
	defer srv.Close()
	t.Setenv(apiKeyEnv, "")

	cases := map[string][]string{
		"no input":           {"-api-key", "k"},
		"bad format":         {"-api-key", "k", "-format", "wav", small},
		"not mp3":            {"-api-key", "k", wav},
		"too large":          {"-api-key", "k", big},
		"negative start":     {"-api-key", "k", "-start", "-1", small},
		"-o with many files": {"-api-key", "k", "-o", "x.zip", small, other},
		"missing key":        {small},
	}
	for name, args := range cases {
		code, _, _ := runIn(t, dir, append([]string{"-api-url", srv.URL}, args...)...)
		if code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestKeyFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	in := tempMP3(t, dir, "a.mp3", 10)
	srv, req, _ := fakeAPI(t, 200, "PK", map[string]string{"X-Result-Name": "a_sfx.zip"})
	t.Setenv(apiKeyEnv, "envkey")
	t.Setenv(apiURLEnv, srv.URL)

	if code, _, errOut := runIn(t, dir, "-quiet", in); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if req.Header.Get("Authorization") != "Bearer envkey" {
		t.Errorf("auth = %q", req.Header.Get("Authorization"))
	}
}
