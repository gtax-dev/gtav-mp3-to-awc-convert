// Command gtav-mp3-to-awc-convert turns MP3 files into GTA V sounds (an AWC wave bank plus its sound
// metadata) through the gtax.dev API, packaged as a ready-to-run FiveM resource or a singleplayer
// dlc.rpf.
//
//	gtav-mp3-to-awc-convert horn.mp3                        # -> horn_sfx.zip (FiveM resource)
//	gtav-mp3-to-awc-convert -format dlc-rpf horn.mp3        # -> horn/dlc.rpf (singleplayer)
//	gtav-mp3-to-awc-convert -start 2 -length 5 song.mp3     # keep 5 seconds starting at 0:02
//	gtav-mp3-to-awc-convert sounds/*.mp3                    # one pack per file
//
// Authentication uses your gtax.dev API key (gtax_dglb_...), the same key as the other gtax.dev
// tools. Set it with -api-key or the GTAX_API_KEY environment variable.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBase = "https://public-mp3-to-awc.gtax.dev"
	apiKeyEnv      = "GTAX_API_KEY"
	apiURLEnv      = "GTAX_API_URL"
	// maxUploadBytes matches the API's per-request limit, so oversized files fail before uploading.
	maxUploadBytes = 8 << 20
	toolName       = "gtav-mp3-to-awc-convert"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "dev"

type options struct {
	format, project, bank, sound, output string
	start, length                        float64
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run returns a process exit code (0 ok, 1 runtime error, 2 usage error) instead of calling os.Exit,
// so tests can drive it directly.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.format, "format", "fivem-zip", "fivem-zip (FiveM resource) or dlc-rpf (singleplayer)")
	fs.StringVar(&o.project, "project", "", "project name (default: the MP3 file name)")
	fs.StringVar(&o.bank, "bank", "", "sound bank name (default: the project name)")
	fs.StringVar(&o.sound, "sound", "", "in-game sound name (default: the bank name)")
	fs.Float64Var(&o.start, "start", 0, "start of the part to keep, in seconds")
	fs.Float64Var(&o.length, "length", 0, "length to keep, in seconds (0 keeps everything after -start)")
	fs.StringVar(&o.output, "o", "", "output path for a single input (default: <project>_sfx.zip, or <project>/dlc.rpf)")
	apiKey := fs.String("api-key", "", "API key (or env "+apiKeyEnv+")")
	apiURL := fs.String("api-url", "", "API base URL (or env "+apiURLEnv+", default "+defaultAPIBase+")")
	timeout := fs.Duration("timeout", 2*time.Minute, "HTTP timeout per file")
	quiet := fs.Bool("quiet", false, "only print the written file paths")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s [options] <file.mp3> [more.mp3 ...]\n\n", toolName)
		fmt.Fprintln(stderr, "Turn MP3 files into GTA V sounds: a FiveM resource zip or a singleplayer dlc.rpf.")
		fmt.Fprintln(stderr, "\nExamples:")
		fmt.Fprintf(stderr, "  %s horn.mp3                     # -> horn_sfx.zip\n", toolName)
		fmt.Fprintf(stderr, "  %s -format dlc-rpf horn.mp3     # -> horn/dlc.rpf\n", toolName)
		fmt.Fprintf(stderr, "  %s -start 2 -length 5 song.mp3  # keep 5 s from 0:02\n", toolName)
		fmt.Fprintf(stderr, "  %s sounds/*.mp3                 # one pack per file\n", toolName)
		fmt.Fprintln(stderr, "\nOptions:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "%s %s\n", toolName, version)
		return 0
	}

	files := fs.Args()
	switch {
	case len(files) == 0:
		fs.Usage()
		return 2
	case o.format != "fivem-zip" && o.format != "dlc-rpf":
		fmt.Fprintf(stderr, "error: -format must be fivem-zip or dlc-rpf, got %q\n", o.format)
		return 2
	case len(files) > 1 && (o.output != "" || o.project != "" || o.bank != "" || o.sound != ""):
		fmt.Fprintln(stderr, "error: -o, -project, -bank and -sound work with one input file; with several files each pack is named after its file")
		return 2
	case o.start < 0 || o.length < 0:
		fmt.Fprintln(stderr, "error: -start and -length cannot be negative")
		return 2
	}
	for _, f := range files {
		if code := validateInput(f, stderr); code != 0 {
			return code
		}
	}
	key := firstNonEmpty(*apiKey, os.Getenv(apiKeyEnv))
	if key == "" {
		fmt.Fprintf(stderr, "error: an API key is required. Set -api-key or %s (your gtax_dglb_ key from https://gtax.dev/api-keys).\n", apiKeyEnv)
		return 2
	}
	base := strings.TrimRight(firstNonEmpty(*apiURL, os.Getenv(apiURLEnv), defaultAPIBase), "/")
	client := &http.Client{Timeout: *timeout}

	failed := 0
	for _, f := range files {
		if err := convertFile(client, base, key, f, o, *quiet, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %s: %v\n", f, err)
			failed++
		}
	}
	if failed > 0 {
		if len(files) > 1 {
			fmt.Fprintf(stderr, "%d of %d files failed\n", failed, len(files))
		}
		return 1
	}
	if !*quiet {
		printInstallHint(stderr, o.format)
	}
	return 0
}

// validateInput checks one input before anything is uploaded: extension, existence and size.
func validateInput(path string, stderr io.Writer) int {
	if !strings.EqualFold(filepath.Ext(path), ".mp3") {
		fmt.Fprintf(stderr, "error: input must be an .mp3 file, got %q\n", path)
		return 2
	}
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: cannot read %q: %v\n", path, err)
		return 1
	}
	if info.IsDir() {
		fmt.Fprintf(stderr, "error: %q is a directory\n", path)
		return 2
	}
	if info.Size() > maxUploadBytes {
		fmt.Fprintf(stderr, "error: %q is %.1f MB; the limit is %d MB. Trim or re-encode it first.\n", path, float64(info.Size())/(1<<20), maxUploadBytes>>20)
		return 2
	}
	return 0
}

func convertFile(client *http.Client, base, key, path string, o options, quiet bool, stdout, stderr io.Writer) error {
	project := firstNonEmpty(o.project, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	fields := map[string]string{"format": o.format, "projectName": project}
	if o.bank != "" {
		fields["bankName"] = o.bank
	}
	if o.sound != "" {
		fields["streamName"] = o.sound
	}
	if o.start > 0 {
		fields["trimStartSec"] = strconv.FormatFloat(o.start, 'f', -1, 64)
	}
	if o.length > 0 {
		fields["trimLengthSec"] = strconv.FormatFloat(o.length, 'f', -1, 64)
	}
	if !quiet {
		fmt.Fprintf(stderr, "Converting %s ...\n", path)
	}

	resp, err := upload(client, base+"/convert", key, path, fields)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("%s: %s", resp.Status, errorMessage(resp.Body))
		if after := resp.Header.Get("Retry-After"); after != "" {
			msg += fmt.Sprintf(" (retry after %ss)", after)
		}
		return errors.New(msg)
	}

	out := outputPath(o, project, resp.Header.Get("X-Result-Name"))
	if dir := filepath.Dir(out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	n, err := writeFile(out, resp.Body)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s (%d KB)\n", out, (n+1023)/1024)
	if left := resp.Header.Get("X-RateLimit-Remaining"); left != "" && !quiet {
		fmt.Fprintf(stderr, "conversions left today: %s\n", left)
	}
	return nil
}

// upload streams a multipart POST with the MP3 read straight from disk.
func upload(client *http.Client, url, key, path string, fields map[string]string) (*http.Response, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			for k, v := range fields {
				if err := mw.WriteField(k, v); err != nil {
					return err
				}
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			part, err := mw.CreateFormFile("mp3", filepath.Base(path))
			if err != nil {
				return err
			}
			_, err = io.Copy(part, f)
			return err
		}()
		if cerr := mw.Close(); err == nil {
			err = cerr
		}
		_ = pw.CloseWithError(err)
	}()

	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", toolName+"/"+version)
	return client.Do(req)
}

var nonNameChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// projectFolder mirrors the API's name cleanup (keep [A-Za-z0-9_], lowercase, max 48 chars) so a
// dlc.rpf lands in the folder the game expects: dlcpacks/<project>/dlc.rpf.
func projectFolder(project string) string {
	name := strings.ToLower(nonNameChars.ReplaceAllString(project, ""))
	if name == "" {
		name = "starterbank"
	}
	if len(name) > 48 {
		name = name[:48]
	}
	return name
}

// outputPath picks where to write: -o wins; a dlc.rpf goes into <project>/dlc.rpf; a FiveM zip uses the
// server's file name. Server-supplied names are reduced to a base name so they never leave the
// working directory.
func outputPath(o options, project, resultName string) string {
	if o.output != "" {
		return o.output
	}
	if o.format == "dlc-rpf" {
		return filepath.Join(projectFolder(project), "dlc.rpf")
	}
	if name := filepath.Base(strings.TrimSpace(resultName)); name != "" && name != "." && name != "/" {
		return name
	}
	return projectFolder(project) + "_sfx.zip"
}

func writeFile(path string, r io.Reader) (int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path) // never leave a truncated file behind
		return n, fmt.Errorf("write %s: %w", path, err)
	}
	return n, nil
}

// errorMessage prefers the API's {"error":"..."} body and falls back to the raw text.
func errorMessage(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 64<<10))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func printInstallHint(w io.Writer, format string) {
	if format == "dlc-rpf" {
		fmt.Fprintln(w, "Singleplayer: copy the folder into mods/update/x64/dlcpacks/ and add it to dlclist.xml.")
		return
	}
	fmt.Fprintln(w, "FiveM: unzip into resources/, add `ensure <name>_sfx` to server.cfg, then use /playsound2d in game.")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
