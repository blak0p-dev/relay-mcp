package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireStagesOnlyVerifiedRelayArchive(t *testing.T) {
	archive := tarball(t, []tarEntry{{name: "relay", body: "new binary"}})
	sum := sha256.Sum256(archive)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/relay.tar.gz":
			_, _ = w.Write(archive)
		case "/checksums.txt":
			_, _ = fmt.Fprintf(w, "%x  relay.tar.gz\n", sum)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	staged, err := Acquire(context.Background(), Request{
		ArtifactURL: server.URL + "/relay.tar.gz", ChecksumURL: server.URL + "/checksums.txt",
		Target: "relay", StagingDir: t.TempDir(), Client: server.Client(),
	})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	got, err := os.ReadFile(staged)
	if err != nil || string(got) != "new binary" {
		t.Fatalf("staged bytes = %q, %v", got, err)
	}
}

func TestAcquireRejectsUnverifiedOrUnsafeInputs(t *testing.T) {
	archive := tarball(t, []tarEntry{{name: "relay", body: "binary"}})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://example.test/relay.tar.gz", http.StatusFound)
			return
		}
		if r.URL.Path == "/bad" {
			_, _ = w.Write(archive)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("0", 64) + "  bad.tar.gz\n"))
	}))
	defer server.Close()
	offline := httptest.NewTLSServer(http.NotFoundHandler())
	offlineURL, offlineClient := offline.URL, offline.Client()
	offline.Close()
	cases := []struct {
		name    string
		request Request
	}{
		{"non HTTPS", Request{ArtifactURL: "http://example.test/relay.tar.gz", ChecksumURL: "https://example.test/checksums", Target: "relay", StagingDir: t.TempDir()}},
		{"redirect downgrade", Request{ArtifactURL: server.URL + "/redirect", ChecksumURL: server.URL + "/checksums", Target: "relay", StagingDir: t.TempDir(), Client: server.Client()}},
		{"checksum mismatch", Request{ArtifactURL: server.URL + "/bad", ChecksumURL: server.URL + "/checksums", Target: "relay", StagingDir: t.TempDir(), Client: server.Client()}},
		{"offline", Request{ArtifactURL: offlineURL + "/relay.tar.gz", ChecksumURL: offlineURL + "/checksums", Target: "relay", StagingDir: t.TempDir(), Client: offlineClient}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Acquire(context.Background(), tt.request); err == nil {
				t.Fatal("Acquire() succeeded")
			}
		})
	}
}

func TestAcquireRejectsUnsafeArchiveMembers(t *testing.T) {
	cases := []struct {
		name    string
		entries []tarEntry
		limit   int64
	}{
		{"documentation decoy", []tarEntry{{name: "README.sh", body: "x"}}, 1 << 20},
		{"cmake decoy", []tarEntry{{name: "CMakeLists.txt", body: "x"}}, 1 << 20},
		{"markdown decoy", []tarEntry{{name: "guide.mdx", body: "x"}}, 1 << 20},
		{"requirements decoy", []tarEntry{{name: "requirements.txt", body: "x"}}, 1 << 20},
		{"traversal", []tarEntry{{name: "../relay", body: "x"}}, 1 << 20},
		{"duplicate relay", []tarEntry{{name: "relay", body: "x"}, {name: "relay", body: "y"}}, 1 << 20},
		{"link", []tarEntry{{name: "relay", link: "other"}}, 1 << 20},
		{"size limit", []tarEntry{{name: "relay", body: "too big"}}, 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := extract(bytes.NewReader(tarball(t, tt.entries)), "relay", t.TempDir(), tt.limit); err == nil {
				t.Fatal("extract() succeeded")
			}
		})
	}
}

func TestActivateRollsBackAndDefersWindowsLock(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "relay")
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(destination, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := activate(staged, destination, "unix", func(old, new string) error {
		if old == staged {
			return errors.New("disk full")
		}
		return os.Rename(old, new)
	})
	if err == nil || result.Completed {
		t.Fatalf("rollback result = %#v, %v", result, err)
	}
	got, _ := os.ReadFile(destination)
	if string(got) != "old" {
		t.Fatalf("destination = %q", got)
	}
	result, err = activate(staged, destination, "windows", func(_, _ string) error { return errors.New("sharing violation") })
	if err != nil || !result.Deferred || result.Completed {
		t.Fatalf("windows result = %#v, %v", result, err)
	}
}

func TestActivateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "relay")
	staged := filepath.Join(dir, "staged")
	_ = os.WriteFile(destination, []byte("same"), 0o755)
	_ = os.WriteFile(staged, []byte("same"), 0o755)
	result, err := activate(staged, destination, "unix", os.Rename)
	if err != nil || !result.AlreadyCurrent {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

type tarEntry struct{ name, body, link string }

func tarball(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		typ := byte(tar.TypeReg)
		if entry.link != "" {
			typ = tar.TypeSymlink
		}
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o755, Typeflag: typ, Size: int64(len(entry.body)), Linkname: entry.link}); err != nil {
			t.Fatal(err)
		}
		if entry.body != "" {
			_, _ = tw.Write([]byte(entry.body))
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
