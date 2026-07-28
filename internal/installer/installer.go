// Package installer verifies and stages Relay releases before activation.
package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"strings"
)

const defaultArchiveLimit int64 = 64 << 20

type Request struct {
	ArtifactURL, ChecksumURL, Target, StagingDir string
	MaxBytes                                     int64
	Client                                       *http.Client
}

// Acquire downloads an HTTPS artifact, verifies its published checksum, and stages its sole Relay executable.
func Acquire(ctx context.Context, request Request) (string, error) {
	if request.Target != "relay" && request.Target != "relay.exe" {
		return "", errors.New("invalid Relay target")
	}
	archive, err := fetch(ctx, request.Client, request.ArtifactURL, limit(request.MaxBytes))
	if err != nil {
		return "", err
	}
	checksums, err := fetch(ctx, request.Client, request.ChecksumURL, 1<<20)
	if err != nil {
		return "", err
	}
	name := path.Base(mustURL(request.ArtifactURL).Path)
	if !validChecksum(checksums, name, archive) {
		return "", errors.New("release checksum verification failed")
	}
	return extract(bytes.NewReader(archive), request.Target, request.StagingDir, limit(request.MaxBytes))
}

func limit(value int64) int64 {
	if value > 0 {
		return value
	}
	return defaultArchiveLimit
}

func fetch(ctx context.Context, client *http.Client, raw string, max int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("release URLs must use HTTPS")
	}
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(req *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	response, err := copy.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		redirect, err := response.Location()
		if err != nil || redirect.Scheme != "https" {
			return nil, errors.New("release redirect must use HTTPS")
		}
		return fetch(ctx, &copy, redirect.String(), max)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch release: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, errors.New("release download exceeds limit")
	}
	return body, nil
}

func mustURL(raw string) *url.URL { u, _ := url.Parse(raw); return u }

func validChecksum(text []byte, asset string, data []byte) bool {
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	for _, line := range strings.Split(string(text), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == sum && strings.TrimPrefix(fields[len(fields)-1], "*") == asset {
			return true
		}
	}
	return false
}

func extract(archive io.Reader, target, directory string, max int64) (string, error) {
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return "", errors.New("release archive must be gzip tar")
	}
	defer gzipReader.Close()
	reader := tar.NewReader(io.LimitReader(gzipReader, max+1))
	var staged string
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			return "", errors.New("release archive contains non-regular member")
		}
		if header.Name != target {
			return "", errors.New("release archive must contain exactly one root Relay executable")
		}
		if staged != "" {
			return "", errors.New("release archive has duplicate Relay executable")
		}
		file, err := os.CreateTemp(directory, ".relay-stage-*")
		if err != nil {
			return "", err
		}
		staged = file.Name()
		written, err := io.Copy(file, io.LimitReader(reader, max-total+1))
		total += written
		closeErr := file.Close()
		if err != nil || closeErr != nil || total > max {
			_ = os.Remove(staged)
			return "", errors.New("release archive exceeds staging limit")
		}
		if err := os.Chmod(staged, 0o755); err != nil {
			_ = os.Remove(staged)
			return "", err
		}
	}
	if staged == "" {
		return "", errors.New("release archive has no Relay executable")
	}
	return staged, nil
}

type Result struct {
	Completed, Deferred, AlreadyCurrent bool
	Backup                              string
}

// Activate atomically activates a staged binary. Windows sharing violations leave staging intact for deferred handling.
func Activate(staged, destination string) (Result, error) {
	return activate(staged, destination, runtime.GOOS, os.Rename)
}

func activate(staged, destination, platform string, rename func(string, string) error) (Result, error) {
	stagedBytes, err := os.ReadFile(staged)
	if err != nil {
		return Result{}, err
	}
	if current, err := os.ReadFile(destination); err == nil && string(current) == string(stagedBytes) {
		return Result{AlreadyCurrent: true}, nil
	}
	if platform == "windows" {
		if err := rename(staged, destination); err != nil {
			return Result{Deferred: true}, nil
		}
		return Result{Completed: true}, nil
	}
	backup := destination + ".previous"
	hadDestination := true
	if err := rename(destination, backup); err != nil {
		if os.IsNotExist(err) {
			hadDestination = false
		} else {
			return Result{}, err
		}
	}
	if err := rename(staged, destination); err != nil {
		if hadDestination {
			_ = rename(backup, destination)
		}
		return Result{Backup: backup}, err
	}
	return Result{Completed: true, Backup: backup}, nil
}
