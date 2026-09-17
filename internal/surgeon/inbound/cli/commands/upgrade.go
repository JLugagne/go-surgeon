package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/datapointchris/goselfupdate"
	"github.com/spf13/cobra"
)

const (
	upgradeRepoOwner = "JLugagne"
	upgradeRepoName  = "go-surgeon"
	upgradeBinary    = "go-surgeon"
)

// NewUpgradeCommand builds the `upgrade` command. It replaces the running
// binary with a release fetched from GitHub, verifying the download against
// the published checksums.txt before anything is written.
func NewUpgradeCommand(currentVersion string) *cobra.Command {
	var (
		checkOnly  bool
		targetVer  string
		prerelease bool
	)

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the go-surgeon binary to the latest release",
		Long: `Upgrade fetches the latest go-surgeon release from GitHub, verifies the
checksum against checksums.txt, and atomically replaces the current binary.

Pass --check to only report whether a newer version is available.
Pass --version vX.Y.Z to install a specific version (including downgrades).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg := goselfupdate.Config{
				Owner:           upgradeRepoOwner,
				Repo:            upgradeRepoName,
				Binary:          upgradeBinary,
				Version:         currentVersion,
				AllowPrerelease: prerelease,
			}

			if targetVer != "" {
				return runPinnedUpgrade(ctx, currentVersion, targetVer, checkOnly, cfg)
			}

			if checkOnly {
				result, err := goselfupdate.Check(ctx, cfg)
				if errors.Is(err, goselfupdate.ErrDevBuild) {
					return fmt.Errorf("cannot check for updates from a development build (%q)", currentVersion)
				}
				if err != nil {
					return err
				}
				if !result.UpdateAvailable() {
					fmt.Printf("go-surgeon %s is up to date\n", currentVersion)
					return nil
				}
				fmt.Printf("current: %s\nlatest:  %s\n", result.From, result.To)
				return nil
			}

			result, err := goselfupdate.Update(ctx, cfg)
			if errors.Is(err, goselfupdate.ErrDevBuild) {
				return fmt.Errorf("cannot upgrade a development build (%q); install a released binary first", currentVersion)
			}
			if err != nil {
				return err
			}
			if !result.Applied {
				fmt.Printf("go-surgeon %s is already up to date\n", currentVersion)
				return nil
			}
			fmt.Printf("upgraded go-surgeon %s -> %s\n", result.From, result.To)
			return nil
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "only report whether a newer version is available")
	cmd.Flags().StringVar(&targetVer, "version", "", "install a specific version (e.g. v0.8.0)")
	cmd.Flags().BoolVar(&prerelease, "prerelease", false, "include prereleases when detecting the latest version")

	return cmd
}

// runPinnedUpgrade installs one exact release tag. goselfupdate only moves
// forwards, so the comparison floor is lowered to allow reinstalling and
// downgrading to the requested tag.
func runPinnedUpgrade(ctx context.Context, currentVersion, targetVer string, checkOnly bool, cfg goselfupdate.Config) error {
	src, err := newPinnedSource(upgradeRepoOwner, upgradeRepoName, targetVer)
	if err != nil {
		return err
	}
	cfg.Source = src

	release, err := src.LatestRelease(ctx)
	if err != nil {
		return err
	}
	target := goselfupdate.Canonical(release.Tag)

	if checkOnly {
		fmt.Printf("current: %s\ntarget:  %s\n", goselfupdate.Canonical(currentVersion), target)
		return nil
	}

	cfg.Version = "v0.0.0"
	result, err := goselfupdate.Update(ctx, cfg)
	if err != nil {
		return err
	}
	if !result.Applied {
		fmt.Printf("go-surgeon %s is already installed\n", target)
		return nil
	}
	fmt.Printf("upgraded go-surgeon %s -> %s\n", currentVersion, target)
	return nil
}

// pinnedSource is a goselfupdate.Source that resolves one exact release tag
// instead of the latest one. It backs `upgrade --version`.
type pinnedSource struct {
	owner   string
	repo    string
	tag     string
	apiBase string
	token   string
	client  *http.Client

	loaded  bool
	release goselfupdate.Release
}

func newPinnedSource(owner, repo, tag string) (*pinnedSource, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, fmt.Errorf("--version requires a tag, e.g. v1.2.7")
	}
	base := os.Getenv("GITHUB_API_URL")
	if base == "" {
		base = "https://api.github.com"
	}
	return &pinnedSource{
		owner:   owner,
		repo:    repo,
		tag:     tag,
		apiBase: strings.TrimRight(base, "/"),
		token:   firstNonEmpty(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN")),
		client:  &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// LatestRelease implements goselfupdate.Source.
func (s *pinnedSource) LatestRelease(ctx context.Context) (goselfupdate.Release, error) {
	if s.loaded {
		return s.release, nil
	}
	tags := []string{s.tag}
	if !strings.HasPrefix(s.tag, "v") {
		tags = append(tags, "v"+s.tag)
	}
	var lastErr error
	for _, tag := range tags {
		release, err := s.releaseByTag(ctx, tag)
		if err == nil {
			s.release, s.loaded = release, true
			return release, nil
		}
		lastErr = err
	}
	return goselfupdate.Release{}, lastErr
}

func (s *pinnedSource) releaseByTag(ctx context.Context, tag string) (goselfupdate.Release, error) {
	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", s.apiBase, s.owner, s.repo, tag)
	if err := s.getJSON(ctx, endpoint, &payload); err != nil {
		return goselfupdate.Release{}, err
	}
	release := goselfupdate.Release{Tag: payload.TagName}
	for _, asset := range payload.Assets {
		release.Assets = append(release.Assets, goselfupdate.Asset{
			Name: asset.Name,
			URL:  asset.URL,
			Size: asset.Size,
		})
	}
	return release, nil
}

// Download implements goselfupdate.Source.
func (s *pinnedSource) Download(ctx context.Context, asset goselfupdate.Asset) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", asset.Name, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func (s *pinnedSource) getJSON(ctx context.Context, endpoint string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("query release %s: %w", s.tag, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("release %s: %s: %s", s.tag, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("decode release %s: %w", s.tag, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
