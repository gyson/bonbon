package instance

import (
	"os"
	"path/filepath"
	"testing"

	"bonbon/internal/protocol"
)

func TestShutdownPreservesReplacementDescriptor(t *testing.T) {
	for _, changed := range []string{"unchanged", "missing", "malformed", "replacement", "replaced-directory"} {
		t.Run(changed, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "instance")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			owner := protocol.ServerInfo{DataDir: directory, Instance: "original", Port: 1234}
			if err := Publish(owner); err != nil {
				t.Fatal(err)
			}
			switch changed {
			case "missing":
				if err := Remove(directory); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(filepath.Join(directory, "server.json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "replacement", "replaced-directory":
				if changed == "replaced-directory" {
					if err := os.Rename(directory, directory+"-old"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(directory, 0700); err != nil {
						t.Fatal(err)
					}
				}
				replacement := owner
				replacement.Instance = "replacement"
				if err := Publish(replacement); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(directory, "server.json")
			before, _ := os.ReadFile(path)
			err := RemoveOwned(owner)
			if changed == "malformed" {
				if err == nil {
					t.Fatal("accepted a malformed descriptor")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if changed == "unchanged" || changed == "missing" {
				if !os.IsNotExist(err) {
					t.Fatalf("original descriptor remains: %s, %v", after, err)
				}
			} else if err != nil || string(before) != string(after) {
				t.Fatalf("shutdown changed a replacement descriptor: %s, %v", after, err)
			}
		})
	}
}
