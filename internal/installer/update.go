package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type ArchiveFormat string

const (
	TarGz ArchiveFormat = "tar.gz"
	Zip   ArchiveFormat = "zip"
)

type Release struct {
	Version, ArtifactURL, ChecksumURL, Target string
	Format                                    ArchiveFormat
}

type LatestResolver interface {
	Latest(context.Context, string, string) (Release, error)
}

type GitHubLatest struct {
	Endpoint string
	Client   *http.Client
}

func (r GitHubLatest) Latest(ctx context.Context, goos, goarch string) (Release, error) {
	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = "https://api.github.com/repos/blak0p/relay-mcp/releases/latest"
	}
	body, err := fetch(ctx, r.Client, endpoint, 1<<20)
	if err != nil {
		return Release{}, err
	}
	var response struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Release{}, fmt.Errorf("decode latest release: %w", err)
	}
	version := strings.TrimPrefix(response.TagName, "v")
	if version == "" || strings.Contains(version, "/") {
		return Release{}, errors.New("latest release has invalid version")
	}
	format, target := TarGz, "relay"
	if goos == "windows" {
		format, target = Zip, "relay.exe"
	}
	asset := fmt.Sprintf("relay_%s_%s_%s.%s", version, goos, goarch, format)
	var artifactURL, checksumURL string
	for _, candidate := range response.Assets {
		if candidate.Name != asset && candidate.Name != "checksums.txt" {
			continue
		}
		if !isHTTPS(candidate.URL) {
			return Release{}, errors.New("latest release asset URL must use HTTPS")
		}
		if candidate.Name == asset {
			if artifactURL != "" {
				return Release{}, errors.New("latest release has duplicate platform asset")
			}
			artifactURL = candidate.URL
		} else {
			if checksumURL != "" {
				return Release{}, errors.New("latest release has duplicate checksums")
			}
			checksumURL = candidate.URL
		}
	}
	if artifactURL == "" || checksumURL == "" {
		return Release{}, errors.New("latest release is missing required platform asset or checksums")
	}
	return Release{Version: version, ArtifactURL: artifactURL, ChecksumURL: checksumURL, Target: target, Format: format}, nil
}

type UpdateResult struct {
	Version, StagedPath string
	Activation          Result
}

type Updater struct {
	Resolver   LatestResolver
	Acquire    func(context.Context, Request) (string, error)
	Activate   func(string, string) (Result, error)
	Executable func() (string, error)
}

func (u Updater) Update(ctx context.Context) (UpdateResult, error) {
	if u.Resolver == nil {
		u.Resolver = GitHubLatest{}
	}
	if u.Acquire == nil {
		u.Acquire = Acquire
	}
	if u.Activate == nil {
		u.Activate = Activate
	}
	if u.Executable == nil {
		u.Executable = os.Executable
	}
	release, err := u.Resolver.Latest(ctx, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("resolve latest Relay release: %w", err)
	}
	destination, err := u.Executable()
	if err != nil {
		return UpdateResult{}, fmt.Errorf("locate running Relay executable: %w", err)
	}
	staged, err := u.Acquire(ctx, Request{ArtifactURL: release.ArtifactURL, ChecksumURL: release.ChecksumURL, Target: release.Target, Format: release.Format, StagingDir: filepath.Dir(destination)})
	if err != nil {
		return UpdateResult{Version: release.Version}, fmt.Errorf("stage verified Relay release: %w", err)
	}
	activation, err := u.Activate(staged, destination)
	if err != nil {
		_ = os.Remove(staged)
		return UpdateResult{Version: release.Version, StagedPath: staged, Activation: activation}, fmt.Errorf("activate Relay release: %w", err)
	}
	return UpdateResult{Version: release.Version, StagedPath: staged, Activation: activation}, nil
}

func isHTTPS(raw string) bool {
	return strings.HasPrefix(raw, "https://")
}
