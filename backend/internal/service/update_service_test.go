package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type mockUpdateCache struct {
	data    string
	setData string
	setTTL  time.Duration
}

func (c *mockUpdateCache) GetUpdateInfo(ctx context.Context) (string, error) {
	if c.data == "" {
		return "", errors.New("cache miss")
	}
	return c.data, nil
}

func (c *mockUpdateCache) SetUpdateInfo(ctx context.Context, data string, ttl time.Duration) error {
	c.setData = data
	c.setTTL = ttl
	return nil
}

type mockUpdateGitHubClient struct {
	release        *GitHubRelease
	apiBaseURL     string
	repo           string
	fetchCalls     int
	downloadCalled bool
}

func (c *mockUpdateGitHubClient) FetchLatestRelease(ctx context.Context, apiBaseURL, repo string) (*GitHubRelease, error) {
	c.fetchCalls++
	c.apiBaseURL = apiBaseURL
	c.repo = repo
	if c.release == nil {
		return nil, errors.New("missing release")
	}
	return c.release, nil
}

func (c *mockUpdateGitHubClient) DownloadFile(ctx context.Context, url, dest string, maxSize int64) error {
	c.downloadCalled = true
	return errors.New("download should not be reached")
}

func (c *mockUpdateGitHubClient) FetchChecksumFile(ctx context.Context, url string) ([]byte, error) {
	return nil, errors.New("checksum should not be reached")
}

func TestUpdateServiceUsesDefaultUpdateSource(t *testing.T) {
	client := &mockUpdateGitHubClient{release: &GitHubRelease{TagName: "v1.2.0"}}
	cache := &mockUpdateCache{}
	svc := NewUpdateService(cache, client, "1.0.0", "release", config.UpdateConfig{})

	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.True(t, info.HasUpdate)
	require.Equal(t, config.DefaultUpdateSourceAPIBaseURL, client.apiBaseURL)
	require.Equal(t, config.DefaultUpdateSourceRepository, client.repo)
	require.NotNil(t, info.Source)
	require.Equal(t, config.DefaultUpdateSourceRepository, info.Source.Repository)
	require.Contains(t, cache.setData, "github_release|Wei-Shaw/sub2api|https://api.github.com")
}

func TestUpdateServiceUsesConfiguredUpdateSource(t *testing.T) {
	client := &mockUpdateGitHubClient{release: &GitHubRelease{TagName: "v1.3.0"}}
	cache := &mockUpdateCache{}
	svc := NewUpdateService(cache, client, "1.0.0", "release", config.UpdateConfig{Source: config.UpdateSourceConfig{
		Repository:           "zoand/sub2api",
		APIBaseURL:           "https://github.example.com/api/v3",
		AllowedDownloadHosts: []string{"github.example.com"},
	}})

	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "https://github.example.com/api/v3", client.apiBaseURL)
	require.Equal(t, "zoand/sub2api", client.repo)
	require.Equal(t, "zoand/sub2api", info.Source.Repository)
	require.Contains(t, cache.setData, "github_release|zoand/sub2api|https://github.example.com/api/v3")
}

func TestUpdateServiceIgnoresCacheFromDifferentSource(t *testing.T) {
	cache := &mockUpdateCache{data: `{"latest":"9.9.9","release_info":{"name":"stale"},"timestamp":4102444800,"source_id":"github_release|Wei-Shaw/sub2api|https://api.github.com"}`}
	client := &mockUpdateGitHubClient{release: &GitHubRelease{TagName: "v1.4.0"}}
	svc := NewUpdateService(cache, client, "1.0.0", "release", config.UpdateConfig{Source: config.UpdateSourceConfig{
		Repository:           "zoand/sub2api",
		APIBaseURL:           "https://api.github.com",
		AllowedDownloadHosts: []string{"github.com"},
	}})

	info, err := svc.CheckUpdate(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "1.4.0", info.LatestVersion)
	require.False(t, info.Cached)
	require.Equal(t, 1, client.fetchCalls)
}

func TestValidateDownloadURLWithConfiguredHosts(t *testing.T) {
	require.NoError(t, validateDownloadURL("https://download.github.example.com/assets/sub2api.tar.gz", []string{"github.example.com"}))
	require.NoError(t, validateDownloadURL("https://github.example.com/assets/sub2api.tar.gz", []string{"*.github.example.com"}))

	err := validateDownloadURL("https://evil.example.com/assets/sub2api.tar.gz", []string{"github.example.com"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "untrusted host")

	err = validateDownloadURL("http://github.example.com/assets/sub2api.tar.gz", []string{"github.example.com"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "only HTTPS")
}

func TestPerformUpdateRequiresChecksumWhenConfigured(t *testing.T) {
	archiveName := fmt.Sprintf("sub2api_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	client := &mockUpdateGitHubClient{release: &GitHubRelease{
		TagName: "v1.5.0",
		Assets: []GitHubAsset{{
			Name:               archiveName,
			BrowserDownloadURL: "https://github.com/zoand/sub2api/releases/download/v1.5.0/" + archiveName,
		}},
	}}
	svc := NewUpdateService(&mockUpdateCache{}, client, "1.0.0", "release", config.UpdateConfig{Source: config.UpdateSourceConfig{
		Repository:           "zoand/sub2api",
		APIBaseURL:           "https://api.github.com",
		AllowedDownloadHosts: []string{"github.com"},
		ChecksumRequired:     true,
	}})

	err := svc.PerformUpdate(context.Background())
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "checksum")
	require.False(t, client.downloadCalled)
}
