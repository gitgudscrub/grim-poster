package main

import "testing"

func TestSignedReleasePath(t *testing.T) {
	good := []string{"releases/BOTC-Grim-Poster-0.1.49.zip", "releases/grim-poster-v1.zip"}
	for _, asset := range good {
		if err := validateSignedAsset(asset); err != nil {
			t.Errorf("valid relative signed asset %q rejected: %v", asset, err)
		}
	}
	bad := []string{"https://evil.example/file.zip", "//evil.example/file.zip", "releases/../evil.zip", "releases/a/b.zip", "releases/%2e%2e.zip", "releases/%2fsecret.zip", "releases/a.zip?bad=1", "releases/a.zip#fragment", "releases/a.exe", "releases/", "releases/\\evil.zip"}
	for _, asset := range bad {
		if err := validateSignedAsset(asset); err == nil {
			t.Errorf("unsafe signed asset %q was accepted", asset)
		}
	}
}
