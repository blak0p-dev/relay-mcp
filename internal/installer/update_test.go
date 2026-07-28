package installer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubLatestSelectsExactHTTPSPlatformAssets(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","assets":[
			{"name":"relay_1.2.3_linux_amd64.tar.gz","browser_download_url":"https://example.test/linux.tar.gz"},
			{"name":"relay_1.2.3_linux_arm64.tar.gz","browser_download_url":"https://example.test/linux-arm.tar.gz"},
			{"name":"relay_1.2.3_windows_amd64.zip","browser_download_url":"https://example.test/windows.zip"},
			{"name":"checksums.txt","browser_download_url":"https://example.test/checksums.txt"}
		]}`))
	}))
	defer server.Close()

	release, err := (GitHubLatest{Endpoint: server.URL, Client: server.Client()}).Latest(context.Background(), "linux", "amd64")
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if release.Version != "1.2.3" || release.ArtifactURL != "https://example.test/linux.tar.gz" || release.ChecksumURL != "https://example.test/checksums.txt" || release.Target != "relay" || release.Format != TarGz {
		t.Fatalf("Latest() = %#v", release)
	}

	_, err = (GitHubLatest{Endpoint: server.URL, Client: server.Client()}).Latest(context.Background(), "darwin", "amd64")
	if err == nil || !strings.Contains(err.Error(), "asset") {
		t.Fatalf("Latest() missing platform asset error = %v", err)
	}
}

func TestUpdaterOrdersStepsAndStopsAfterFailure(t *testing.T) {
	for _, tt := range []struct {
		name        string
		acquireErr  error
		activateErr error
		wantCalls   string
	}{
		{"completed", nil, nil, "resolve,acquire,activate"},
		{"acquire failure", errors.New("offline"), nil, "resolve,acquire"},
		{"activation failure", nil, errors.New("locked"), "resolve,acquire,activate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			updater := Updater{
				Resolver: resolverFunc(func(context.Context, string, string) (Release, error) {
					calls = append(calls, "resolve")
					return Release{Version: "1.2.3", ArtifactURL: "https://example.test/relay.tar.gz", ChecksumURL: "https://example.test/checksums.txt", Target: "relay", Format: TarGz}, nil
				}),
				Acquire: func(context.Context, Request) (string, error) {
					calls = append(calls, "acquire")
					return filepath.Join(t.TempDir(), "stage"), tt.acquireErr
				},
				Activate: func(string, string) (Result, error) {
					calls = append(calls, "activate")
					return Result{Completed: true}, tt.activateErr
				},
				Executable: func() (string, error) { return filepath.Join(t.TempDir(), "relay"), nil },
			}
			_, err := updater.Update(context.Background())
			if (err != nil) != (tt.acquireErr != nil || tt.activateErr != nil) {
				t.Fatalf("Update() error = %v", err)
			}
			if got := strings.Join(calls, ","); got != tt.wantCalls {
				t.Fatalf("call order = %q, want %q", got, tt.wantCalls)
			}
		})
	}
}

type resolverFunc func(context.Context, string, string) (Release, error)

func (f resolverFunc) Latest(ctx context.Context, goos, goarch string) (Release, error) {
	return f(ctx, goos, goarch)
}
