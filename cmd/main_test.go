package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadCredentialsFromFileSeparators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.txt")
	contents := "\n  # comment\n" +
		" alice@example.com---pass-one---TOTPONE \n" +
		"bob@example.com----pass---two----TOTPTWO\n" +
		"invalid-line\n" +
		"carol@example.com----pass-three----TOTPTHREE\n" +
		"  # another comment\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCredentialsFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Credential{
		{Email: "alice@example.com", Password: "pass-one", TOTPSecret: "TOTPONE"},
		{Email: "bob@example.com", Password: "pass---two", TOTPSecret: "TOTPTWO"},
		{Email: "carol@example.com", Password: "pass-three", TOTPSecret: "TOTPTHREE"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readCredentialsFromFile() = %#v, want %#v", got, want)
	}
}
