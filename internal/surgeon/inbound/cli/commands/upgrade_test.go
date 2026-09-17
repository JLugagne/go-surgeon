package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/datapointchris/goselfupdate"
)

func TestNewPinnedSourceRejectsEmptyTag(t *testing.T) {
	if _, err := newPinnedSource("JLugagne", "go-surgeon", "   "); err == nil {
		t.Fatal("expected an error for an empty tag")
	}
}

func TestPinnedSourceLatestRelease(t *testing.T) {
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/JLugagne/go-surgeon/releases/tags/v1.2.7" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"tag_name":"v1.2.7","assets":[{"name":"go-surgeon_linux_amd64.tar.gz","browser_download_url":%q,"size":42}]}`, base+"/asset")
	}))
	defer server.Close()
	base = server.URL
	t.Setenv("GITHUB_API_URL", server.URL)

	src, err := newPinnedSource("JLugagne", "go-surgeon", "v1.2.7")
	if err != nil {
		t.Fatalf("newPinnedSource: %v", err)
	}
	release, err := src.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if release.Tag != "v1.2.7" {
		t.Fatalf("tag = %q, want v1.2.7", release.Tag)
	}
	if len(release.Assets) != 1 || release.Assets[0].Name != "go-surgeon_linux_amd64.tar.gz" {
		t.Fatalf("unexpected assets: %+v", release.Assets)
	}
	if release.Assets[0].URL != base+"/asset" {
		t.Fatalf("asset URL = %q, want %q", release.Assets[0].URL, base+"/asset")
	}
}

func TestPinnedSourceAddsVPrefix(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/repos/JLugagne/go-surgeon/releases/tags/v1.2.7" {
			_, _ = w.Write([]byte(`{"tag_name":"v1.2.7"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL)

	src, err := newPinnedSource("JLugagne", "go-surgeon", "1.2.7")
	if err != nil {
		t.Fatalf("newPinnedSource: %v", err)
	}
	release, err := src.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if release.Tag != "v1.2.7" {
		t.Fatalf("tag = %q, want v1.2.7", release.Tag)
	}
	want := []string{
		"/repos/JLugagne/go-surgeon/releases/tags/1.2.7",
		"/repos/JLugagne/go-surgeon/releases/tags/v1.2.7",
	}
	if len(paths) != len(want) {
		t.Fatalf("requested paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

func TestPinnedSourceDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/asset" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("binary-bytes"))
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL)

	src, err := newPinnedSource("JLugagne", "go-surgeon", "v1.2.7")
	if err != nil {
		t.Fatalf("newPinnedSource: %v", err)
	}
	data, err := src.Download(context.Background(), goselfupdate.Asset{Name: "go-surgeon_linux_amd64.tar.gz", URL: server.URL + "/asset"})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(data) != "binary-bytes" {
		t.Fatalf("data = %q, want binary-bytes", data)
	}
}

func TestRunPinnedUpgradeCheckOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/JLugagne/go-surgeon/releases/tags/v1.2.7" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.7"}`))
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL)

	if err := runPinnedUpgrade(context.Background(), "1.2.6", "v1.2.7", true, goselfupdate.Config{}); err != nil {
		t.Fatalf("runPinnedUpgrade: %v", err)
	}
}
