package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalWorkspace(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(root, "alias")
	directory := filepath.Join(root, "workspace")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(alias)
	if err != nil || got != want {
		t.Fatalf("canonical workspace = %q, %v; want %q", got, err, want)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(root, "missing")} {
		if _, err := Canonical(path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}
