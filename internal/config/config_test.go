package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfigRoundtripAndPrivatePermissions(t *testing.T) {
	store := Store{t.TempDir()}
	value := Config{Active: "test", Profiles: map[string]Profile{"test": {APIURL: "https://example.com", Workspace: "w", Name: "name", KeyID: "key"}}}
	if e := store.Save(value); e != nil {
		t.Fatal(e)
	}
	got, e := store.Load()
	if e != nil || got.Active != "test" || got.Profiles["test"].Name != "name" {
		t.Fatal(got, e)
	}
	value.Active = "next"
	if e = store.Save(value); e != nil {
		t.Fatal("atomic replacement:", e)
	}
	info, _ := os.Stat(filepath.Join(store.Directory, "config.json"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("configuration permissions")
	}
}
func TestCorruptConfigIsNotSilentlyReplaced(t *testing.T) {
	store := Store{t.TempDir()}
	_ = os.WriteFile(filepath.Join(store.Directory, "config.json"), []byte("broken"), 0600)
	if _, e := store.Load(); e == nil {
		t.Fatal("accepted broken config")
	}
}
func TestExclusiveCredentialWriteAndKeyIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if e := AtomicWrite(path, []byte("first"), 0600, true); e != nil {
		t.Fatal(e)
	}
	if e := AtomicWrite(path, []byte("second"), 0600, true); e == nil {
		t.Fatal("overwrote credential")
	}
	a := Profile{APIURL: "https://a.test", Workspace: "same", KeyID: "first"}
	b := a
	b.KeyID = "second"
	if CredentialID(a) == CredentialID(b) {
		t.Fatal("profiles share credential slot")
	}
}
