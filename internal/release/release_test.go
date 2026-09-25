package release

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChecksumAndAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev")
	_ = os.WriteFile(path, []byte("old"), 0755)
	data := []byte("new release")
	want := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := replace(path, data, "bad"); err == nil {
		t.Fatal("accepted bad checksum")
	}
	old, _ := os.ReadFile(path)
	if string(old) != "old" {
		t.Fatal("old binary modified")
	}
	if err := replace(path, data, want); err != nil {
		t.Fatal(err)
	}
	installed, _ := os.ReadFile(path)
	if string(installed) != string(data) {
		t.Fatal("replacement failed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0755 {
		t.Fatal("binary not executable")
	}
}
func TestChecksumManifestRejectsMissingAndDuplicates(t *testing.T) {
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("binary")))
	for _, manifest := range []string{"", sum + "  other", sum + "  asset\n" + sum + "  asset", "invalid  asset"} {
		if _, err := checksum([]byte(manifest), "asset"); err == nil {
			t.Fatal("accepted malformed manifest")
		}
	}
	if got, err := checksum([]byte(sum+"  asset\n"), "asset"); err != nil || got != sum {
		t.Fatal(got, err)
	}
}

func TestReleaseDownloadAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dev")
			_ = os.WriteFile(path, []byte("old"), 0755)
			binary := []byte("release")
			sum := fmt.Sprintf("%x", sha256.Sum256(binary))
			calls := 0
			err := installRelease("latest", path, "arm64", io.Discard, func(_ context.Context, url string, _ int64) ([]byte, error) {
				calls++
				switch url {
				case "https://api.github.com/repos/k2b-dev/devstation/releases/latest":
					return []byte(`{"tag_name":"v1.2.3"}`), nil
				case repo + "/releases/download/v1.2.3/checksums.txt":
					return []byte(sum + "  devstation_v1.2.3_linux_arm64\n"), nil
				case repo + "/releases/download/v1.2.3/devstation_v1.2.3_linux_arm64":
					if fail {
						return nil, errors.New("network failure")
					}
					return binary, nil
				default:
					t.Fatalf("unexpected URL %s", url)
					return nil, nil
				}
			})
			got, _ := os.ReadFile(path)
			if calls != 3 {
				t.Fatal(calls)
			}
			if fail {
				if err == nil || string(got) != "old" {
					t.Fatal("failure replaced binary")
				}
			} else if err != nil || string(got) != "release" {
				t.Fatal(err, string(got))
			}
		})
	}
}
func TestRejectUnsafeReleaseTag(t *testing.T) {
	err := installRelease("../oops", "/unused", "amd64", io.Discard, func(context.Context, string, int64) ([]byte, error) { t.Fatal("unexpected download"); return nil, nil })
	if err == nil || !strings.Contains(err.Error(), "vX.Y.Z") {
		t.Fatal(err)
	}
}
