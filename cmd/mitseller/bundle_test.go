package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBundle builds a .tar.gz in memory holding the given name->content
// entries, so the tests can hand the importer a crafted bundle.
func writeBundle(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0600, Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A bundle is data, never a program: untarBundle must drop directories and
// links, and must never write outside the directory it was told to unpack
// into. A bundle arriving from another server is untrusted input.
func TestUntarBundleNeverEscapesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	path := writeBundle(t, map[string]string{
		"manifest.json": `{"version":1}`,
		// a name that tries to climb out of dir
		"../../escaped.txt": "nope",
		// an absolute path
		"/etc/passwd": "nope",
		// a nested path: only the basename is used, so this lands in dir
		"sub/inner.txt": "nested",
	})
	if err := untarBundle(path, dir); err != nil {
		t.Fatalf("untarBundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Errorf("manifest.json was not unpacked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "inner.txt")); err != nil {
		t.Errorf("nested file should land by basename: %v", err)
	}
	// the hostile names are reduced to their basename, so they land inside
	// dir and nowhere else
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err != nil {
		t.Errorf("../../escaped.txt should land as escaped.txt inside dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "passwd")); err != nil {
		t.Errorf("/etc/passwd should land as passwd inside dir: %v", err)
	}
	// nothing was created above dir
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), "..") || strings.Contains(e.Name(), string(os.PathSeparator)) {
			t.Errorf("unexpected entry %q", e.Name())
		}
	}
}

// The manifest is the contract between the two servers: what export-bundle
// writes, import-bundle reads back unchanged.
func TestBundleManifestRoundTrip(t *testing.T) {
	man := bundleManifest{
		Version:     bundleVersion,
		Flavor:      "mirza_pro",
		Domain:      "bot.example.com",
		AdminID:     "12345",
		BotUsername: "mybot",
		BotToken:    "1:token",
		SourceDB:    "mirzabot",
		Cron:        map[string]bool{"test": true, "volume": false},
		Source:      "php",
		PHPDir:      "/var/www/html/mirzabot",
		ExportedAt:  "2026-10-13T00:00:00Z",
	}
	raw, err := json.Marshal(&man)
	if err != nil {
		t.Fatal(err)
	}
	var got bundleManifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != bundleVersion || got.Flavor != "mirza_pro" || got.BotToken != "1:token" {
		t.Errorf("round trip lost data: %+v", got)
	}
	if got.Cron["test"] != true || got.Cron["volume"] != false {
		t.Errorf("cron switches did not round trip: %v", got.Cron)
	}
	// an empty flavour is the legacy one, and must survive as empty
	man.Flavor = ""
	raw, _ = json.Marshal(&man)
	got = bundleManifest{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Flavor != "" {
		t.Errorf("legacy flavour should stay empty, got %q", got.Flavor)
	}
}

// A future bundle must be refused rather than half-read.
func TestBundleVersionIsPinned(t *testing.T) {
	if bundleVersion != 1 {
		t.Fatalf("bundleVersion changed to %d; import-bundle on an older binary would mis-read new bundles", bundleVersion)
	}
}

// untarBundle skips anything that is not a regular file: a symlink or device
// node in a bundle must not be followed onto the host.
func TestUntarBundleSkipsNonRegularEntries(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/shadow", Mode: 0777,
	}); err != nil {
		t.Fatal(err)
	}
	body := "regular"
	if err := tw.WriteHeader(&tar.Header{
		Name: "ok.txt", Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	path := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := untarBundle(path, dir); err != nil {
		t.Fatalf("untarBundle: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "link")); err == nil {
		t.Error("a symlink in a bundle was unpacked")
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.txt")); err != nil {
		t.Errorf("the regular file should still be unpacked: %v", err)
	}
}
