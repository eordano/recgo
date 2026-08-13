//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestICloudSyncedSymlinkedDocuments(t *testing.T) {
	home := t.TempDir()
	cloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "Documents")
	if err := os.MkdirAll(cloud, 0o700); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(home, "Documents")
	if err := os.Symlink(cloud, docs); err != nil {
		t.Fatal(err)
	}
	if !icloudSynced(home, docs) {
		t.Errorf("icloudSynced(%q, %q) = false, want true (Documents -> %s)", home, docs, cloud)
	}
}

func TestICloudSyncedPlainDocuments(t *testing.T) {
	home := t.TempDir()
	docs := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docs, 0o700); err != nil {
		t.Fatal(err)
	}
	if icloudSynced(home, docs) {
		t.Errorf("icloudSynced(%q, %q) = true, want false for a plain directory", home, docs)
	}
}

func TestICloudSyncedMissingDocuments(t *testing.T) {
	home := t.TempDir()
	if icloudSynced(home, filepath.Join(home, "Documents")) {
		t.Error("icloudSynced = true for a nonexistent Documents dir, want false")
	}
}

func TestICloudSyncedSiblingNameNotConfused(t *testing.T) {
	home := t.TempDir()
	sibling := filepath.Join(home, "Library", "Mobile Documents-other")
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(home, "Documents")
	if err := os.Symlink(sibling, docs); err != nil {
		t.Fatal(err)
	}
	if icloudSynced(home, docs) {
		t.Error("icloudSynced = true for a 'Mobile Documents-other' sibling, want false")
	}
}
