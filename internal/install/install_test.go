package install

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fixture struct {
	t                              *testing.T
	root, bin, assets, destination string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("installer uses macOS plutil")
	}
	root := filepath.Join(t.TempDir(), "paths with spaces")
	f := &fixture{t: t, root: root, bin: filepath.Join(root, "tools"), assets: filepath.Join(root, "assets"), destination: filepath.Join(root, "install", "bonbon")}
	for _, dir := range []string{f.bin, f.assets} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	f.write(filepath.Join(f.bin, "uname"), `#!/bin/sh
case "$1" in
    -s) printf '%s\n' "${TEST_OS:-Darwin}" ;;
    -m) printf '%s\n' "${TEST_ARCH:-arm64}" ;;
esac
`)
	f.write(filepath.Join(f.bin, "gh"), "#!/bin/sh\nexit 99\n")
	f.write(filepath.Join(f.bin, "curl"), `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$TEST_ROOT/requests"
config=$(cat)
if [ "${TEST_AUTH_REQUIRED:-0}" = 1 ]; then
    [ "$config" = 'header = "Authorization: Bearer fixture_token"' ] || exit 8
else
    [ -z "$config" ] || exit 9
fi
[ "${TEST_AUTH_FAIL:-0}" = 0 ] || exit 1
output= url= accept=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output) output=$2; shift 2 ;;
        --header) accept=$2; shift 2 ;;
        https://*) url=$1; shift ;;
        *) shift ;;
    esac
done
case "$url" in
    https://api.github.com/repos/gyson/bonbon/releases/latest)
        tag=${TEST_TAG:-v0.0.1} ;;
    https://api.github.com/repos/gyson/bonbon/releases/tags/v*)
        tag=${url##*/} ;;
    https://api.github.com/repos/gyson/bonbon/releases/assets/1)
        [ "$accept" = 'Accept: application/octet-stream' ] || exit 5
        name=$(plutil -extract assets.0.name raw -o - "$TEST_ASSETS/release.json")
        cp "$TEST_ASSETS/$name" "$output"
        exit ;;
    https://api.github.com/repos/gyson/bonbon/releases/assets/2)
        cp "$TEST_ASSETS/checksums.txt" "$output"
        exit ;;
    *) exit 6 ;;
esac
[ "$accept" = 'Accept: application/vnd.github+json' ] || exit 7
if [ "${TEST_BAD_JSON:-0}" = 1 ]; then
    printf 'broken JSON' > "$output"
else
    plutil -replace tag_name -string "$tag" -o "$output" "$TEST_ASSETS/release.json"
fi
`)

	f.release("0.0.1", "arm64", "0.0.1")
	return f
}

func (f *fixture) write(path, data string) {
	f.t.Helper()
	if err := os.WriteFile(path, []byte(data), 0755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) release(version, arch, binaryVersion string) {
	f.t.Helper()
	asset := "bonbon_" + version + "_darwin_" + arch + ".tar.gz"
	path := filepath.Join(f.assets, asset)
	file, err := os.Create(path)
	if err != nil {
		f.t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	binary := "#!/bin/sh\n[ \"$1\" = --version ] || exit 1\nprintf 'bonbon " + binaryVersion + "\\n'\n"
	if err := tw.WriteHeader(&tar.Header{Name: "bonbon", Mode: 0755, Size: int64(len(binary))}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := tw.Write([]byte(binary)); err != nil {
		f.t.Fatal(err)
	}
	for _, err := range []error{tw.Close(), gz.Close(), file.Close()} {
		if err != nil {
			f.t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(f.assets, "checksums.txt"), fmt.Sprintf("%x  %s\n", sha256.Sum256(data), asset))
	metadata, err := json.Marshal(map[string]any{
		"tag_name": "v" + version,
		"assets":   []map[string]any{{"name": asset, "id": 1}, {"name": "checksums.txt", "id": 2}},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(f.assets, "release.json"), string(metadata))
}

func (f *fixture) run(env []string, args ...string) (string, error) {
	f.t.Helper()
	script, err := filepath.Abs("install.sh")
	if err != nil {
		f.t.Fatal(err)
	}
	command := exec.Command("/bin/sh", append([]string{script}, args...)...)
	command.Env = f.environment(env...)
	data, err := command.CombinedOutput()
	return string(data), err
}

func (f *fixture) environment(extra ...string) []string {
	// Use only controlled settings. Never contact GitHub or touch the real home.
	return append([]string{
		"PATH=" + f.bin + ":/usr/bin:/bin", "HOME=" + f.root, "TMPDIR=" + f.root,
		"BONBON_INSTALL_DIR=" + filepath.Dir(f.destination), "TEST_ROOT=" + f.root,
		"TEST_ASSETS=" + f.assets,
	}, extra...)
}

func (f *fixture) existing(version string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.destination), 0755); err != nil {
		f.t.Fatal(err)
	}
	f.write(f.destination, "#!/bin/sh\nprintf 'bonbon "+version+"\\n'\n")
}

func TestInstallAndReplace(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			f := newFixture(t)
			machine := "arm64"
			if arch == "amd64" {
				machine = "x86_64"
			}
			f.release("0.0.1", arch, "0.0.1")
			f.existing("0.0.0")
			// Hold the previous inode open: replacement must use rename, not truncate.
			previous, err := os.Open(f.destination)
			if err != nil {
				t.Fatal(err)
			}
			defer previous.Close()
			output, err := f.run([]string{"TEST_ARCH=" + machine})
			if err != nil || !strings.Contains(output, "Installed BonBon 0.0.1") {
				t.Fatalf("%s: %v", output, err)
			}
			oldData := make([]byte, 200)
			n, err := previous.Read(oldData)
			if err != nil || !strings.Contains(string(oldData[:n]), "0.0.0") {
				t.Fatal("overwrote old inode", err)
			}
			data, err := exec.Command(f.destination, "--version").Output()
			if err != nil || string(data) != "bonbon 0.0.1\n" {
				t.Fatalf("installed: %q %v", data, err)
			}
			entries, err := os.ReadDir(filepath.Dir(f.destination))
			if err != nil || len(entries) != 1 {
				t.Fatal("staging files remain", entries, err)
			}
		})
	}
}

func TestPinnedVersion(t *testing.T) {
	f := newFixture(t)
	f.release("0.0.2", "arm64", "0.0.2")
	output, err := f.run([]string{"TEST_TAG=v9.0.0"}, "--version", "v0.0.2")
	if err != nil || !strings.Contains(output, "Installed BonBon 0.0.2") {
		t.Fatalf("%s: %v", output, err)
	}
	requests, err := os.ReadFile(filepath.Join(f.root, "requests"))
	if err != nil || (!strings.Contains(string(requests), "releases/tags/v0.0.2") || strings.Contains(string(requests), "releases/latest")) {
		t.Fatal("pinned install resolved latest", string(requests), err)
	}
}

func TestFailedInstallPreservesExisting(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*fixture)
		env     []string
		args    []string
		message string
	}{
		{name: "checksum", setup: func(f *fixture) {
			f.write(filepath.Join(f.assets, "checksums.txt"), strings.Repeat("0", 64)+"  bonbon_0.0.1_darwin_arm64.tar.gz\n")
		}, message: "checksum mismatch"},
		{name: "missing checksum", setup: func(f *fixture) { f.write(filepath.Join(f.assets, "checksums.txt"), "") }, message: "missing or invalid checksum"},
		{name: "duplicate checksum", setup: func(f *fixture) {
			p := filepath.Join(f.assets, "checksums.txt")
			data, _ := os.ReadFile(p)
			f.write(p, string(data)+string(data))
		}, message: "duplicate checksum"},
		{name: "wrong executable version", setup: func(f *fixture) { f.release("0.0.1", "arm64", "0.0.2") }, message: "wrong version"},
		{name: "authentication", env: []string{"TEST_AUTH_FAIL=1"}, message: "cannot read release"},
		{name: "platform", env: []string{"TEST_OS=Linux"}, message: "macOS only"},
		{name: "architecture", env: []string{"TEST_ARCH=unknown"}, message: "unsupported macOS architecture"},
		{name: "invalid tag", env: []string{"TEST_TAG=v1/../../escape"}, message: "unsupported release tag"},
		{name: "invalid version", args: []string{"--version", "0.01.0"}, message: "version must have"},
		{name: "missing argument", args: []string{"--dir"}, message: "requires a value"},
		{name: "relative path", args: []string{"--dir", "relative"}, message: "must be absolute"},
		{name: "unknown option", args: []string{"--unknown"}, message: "unknown argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.existing("0.0.0")
			before, _ := os.ReadFile(f.destination)
			if tc.setup != nil {
				tc.setup(f)
			}
			output, err := f.run(tc.env, tc.args...)
			if err == nil || !strings.Contains(output, tc.message) {
				t.Fatalf("%s: %v", output, err)
			}
			after, _ := os.ReadFile(f.destination)
			if string(before) != string(after) {
				t.Fatal("existing executable changed")
			}
		})
	}
}

func TestCheckDoesNotInstall(t *testing.T) {
	cases := []struct{ installed, available, message string }{
		{"0.9.0", "v0.10.0", "Update available"},
		{"0.10.0", "v0.9.0", "installed version is newer"},
		{"0.0.1", "v0.0.1", "Already up to date"},
		{"", "v0.0.1", "not installed"},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			f := newFixture(t)
			if tc.installed != "" {
				f.existing(tc.installed)
			}
			before, _ := os.ReadFile(f.destination)
			output, err := f.run([]string{"TEST_TAG=" + tc.available}, "--check")
			if err != nil || !strings.Contains(output, tc.message) {
				t.Fatalf("%s: %v", output, err)
			}
			after, _ := os.ReadFile(f.destination)
			if string(before) != string(after) {
				t.Fatal("check changed executable")
			}
			requests, _ := os.ReadFile(filepath.Join(f.root, "requests"))
			if strings.Contains(string(requests), "releases/assets/") {
				t.Fatal("check downloaded assets")
			}
			if tc.installed == "" {
				if _, err := os.Stat(filepath.Dir(f.destination)); !os.IsNotExist(err) {
					t.Fatal("check created directory", err)
				}
			}
		})
	}
}

func TestRefusesSymlink(t *testing.T) {
	f := newFixture(t)
	f.existing("0.0.0")
	target := f.destination + "-managed"
	if err := os.Rename(f.destination, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.destination); err != nil {
		t.Fatal(err)
	}
	output, err := f.run(nil)
	if err == nil || !strings.Contains(output, "refusing to replace a symlink") {
		t.Fatalf("%s: %v", output, err)
	}
	if _, err := os.Readlink(f.destination); err != nil {
		t.Fatal("symlink changed", err)
	}
}

func TestTokenIsOptionalAndNotInArguments(t *testing.T) {
	f := newFixture(t)
	output, err := f.run([]string{"GITHUB_TOKEN=fixture_token", "TEST_AUTH_REQUIRED=1"})
	if err != nil {
		t.Fatalf("%s: %v", output, err)
	}
	requests, _ := os.ReadFile(filepath.Join(f.root, "requests"))
	if strings.Contains(string(requests), "fixture_token") {
		t.Fatal("token appeared in curl arguments")
	}
	if strings.Contains(output, "fixture_token") {
		t.Fatal("token appeared in output")
	}
}

func TestInvalidMetadataAndToken(t *testing.T) {
	for _, env := range [][]string{{"TEST_BAD_JSON=1"}, {"GITHUB_TOKEN=bad\ntoken"}} {
		f := newFixture(t)
		f.existing("0.0.0")
		before, _ := os.ReadFile(f.destination)
		output, err := f.run(env)
		if err == nil {
			t.Fatalf("accepted invalid input: %s", output)
		}
		after, _ := os.ReadFile(f.destination)
		if string(before) != string(after) {
			t.Fatal("existing executable changed")
		}
	}
}
