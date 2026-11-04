package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceConfigRejectsConcurrentEdit(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	edited := []byte("administrator edit\n")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceConfig(path, []byte("previous contents\n"), []byte("new hash config\n")); err == nil {
		t.Fatal("concurrent edit was overwritten")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, edited) {
		t.Fatal("concurrent edit was lost")
	}
	temporary, err := filepath.Glob(filepath.Join(directory, ".gamehelm-config-*"))
	if err != nil || len(temporary) != 0 {
		t.Fatal("failed replacement left temporary files")
	}
}

func TestPrepareRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "linked.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, release, err := Prepare(link); err == nil {
		release()
		t.Fatal("accepted symlink for atomic replacement")
	}
}
