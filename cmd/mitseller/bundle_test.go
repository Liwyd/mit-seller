package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Liwyd/mit-seller/internal/config"
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

// export-bundle must put the database in the bundle as dump.sql.gz, which is
// the name cmdImportBundle looks for. A tar entry's size has to be known
// before its header is written, so the dump is staged and then copied in.
// Streaming it straight into the tar writer (what this used to do) left the
// writer with no current entry, so every write was rejected and mysqldump
// died with EPIPE: export-bundle failed for every single bot, and nothing
// noticed because the shell tests drive a fake binary that never dumps.
func TestWriteDumpToTarProducesAReadableDumpSQLGz(t *testing.T) {
	// a mysqldump that writes known bytes and exits 0, so the test needs no
	// database: dumpSQLGz only shells out to it
	bin := t.TempDir()
	fake := filepath.Join(bin, "mysqldump")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho '-- fake dump'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &config.Config{DBUser: "u", DBPass: "p", DBName: "db", DBHost: "localhost", DBPort: "3306"}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := writeDumpToTar(cfg, tw); err != nil {
		t.Fatalf("writeDumpToTar: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}

	// read it back the way the importer does
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := untarBundle(path, dir); err != nil {
		t.Fatalf("untarBundle: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "dump.sql.gz"))
	if err != nil {
		t.Fatalf("the bundle must contain dump.sql.gz: %v", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("dump.sql.gz is not readable gzip: %v", err)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "-- fake dump") {
		t.Errorf("dump.sql.gz does not hold the dump: %q", body)
	}
}
