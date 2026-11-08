package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const officialAPI = "https://api.github.com/repos/ThePandemoniumInstitute/botc-release/releases/latest"
const officialReleases = "https://github.com/ThePandemoniumInstitute/botc-release/releases/latest"
const signedUpdateURL = "https://updates.themavryan.com/"

// Public verification key only. The private signing key MUST NEVER ship with the application.
const updatePublicKeyXML = `<RSAKeyValue><Modulus>rPVTQ9A1W3KLCmRa/SkWQ9V5sNS17sVd4yHDtVBQyAoUPaPVilZgVKuuT/ZHgL4QIbyO4PPKAMrBJ+JifFrOg8qm2MrdOZ1Vuq55iJuqDI1Mf2DRlc5N2NJEz1rAtTmMXF94VvvdckwDkjCZzkSA75P2L0RiABQ2fX6o4wg8YBaarIEXOiHZQq8XbmO3rerYHv55DEEttYWhXmYkHtqmvHDfTAyt53Qadyn4hp9ep1OyO743JbeE3TEA1HFKi89Yp9QM9H1un4lh59thx14xv7ckTMNiBSpRDb7MQHk5iaUCQb9D/5/HFV/lzbkwYBfUWBs/XCQYqS9wZPm1xBBqQM5I3gkzP+H119pN/C/5qbFm/ve4sNhtena2du7Lnb6Sys+HO/YL/UD08FfssToGCIm9HdGu+H37dkqNJ8MkbvcYfu7XwUrvdvfPWg8r/3RQNA9+sCg4wSJ5MpKDWPXxFPjT6KREE1yOkWHD31voXjwx8nAZjIOMVxqwtj+f95BB</Modulus><Exponent>AQAB</Exponent></RSAKeyValue>`

var verRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var shaRE = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type officialRelease struct {
	Version, URL, SHA256 string
	Size                 int64
}
type signedRelease struct {
	ModVersion    string `json:"modVersion"`
	AppVersion    string `json:"appVersion"`
	FormatVersion int    `json:"formatVersion"`
	Asset         string `json:"asset"`
	SHA256        string `json:"sha256"`
	PackageBytes  int64  `json:"packageBytes"`
}
type patchManifest struct {
	AppVersion            string `json:"appVersion"`
	ModVersion            string `json:"modVersion"`
	ExpectedPatchedSha256 string `json:"expectedPatchedSha256"`
	PatchVerification     string `json:"patchVerification"`
}
type installedBOTC struct{ Path, Version, SHA256 string }
type remoteStatus struct {
	Release                                     signedRelease
	Compatible, UpdateAvailable, PatchInstalled bool
}
type stateSnapshot struct {
	Installed *installedBOTC
	Official  *officialRelease
	Remote    *remoteStatus
	Patch     patchManifest
	Error     string
}

func versionCmp(a, b string) int { // strict triplets; invalid versions sort last and fail validation at their entrypoint
	aa := strings.Split(a, ".")
	bb := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(aa) {
			x, _ = strconv.Atoi(aa[i])
		}
		if i < len(bb) {
			y, _ = strconv.Atoi(bb[i])
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
func (s stateSnapshot) patchPresent() bool {
	return s.Installed != nil && s.Patch.AppVersion == s.Installed.Version && strings.EqualFold(s.Patch.ExpectedPatchedSha256, s.Installed.SHA256)
}
func (s stateSnapshot) ready() bool {
	return (s.Patch.PatchVerification == "runtime-injection" && s.patchPresent()) || s.Installed != nil && s.Official != nil && s.Remote != nil && versionCmp(s.Installed.Version, s.Official.Version) >= 0 && s.Remote.Compatible && !s.Remote.UpdateAvailable && s.patchPresent()
}
func (s stateSnapshot) canUseLocalPatch() bool {
	return s.Installed != nil && s.Installed.Version == s.Patch.AppVersion
}
func (s stateSnapshot) canLaunch() bool {
	return s.patchPresent()
}

func readBounded(ctx context.Context, client *http.Client, link string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GrimPoster-Windows-Launcher/0.1.59")
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d from %s", res.StatusCode, link)
	}
	if res.ContentLength > limit {
		return nil, errors.New("response exceeds limit")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("response exceeds limit")
	}
	return b, nil
}
func httpClient() *http.Client {
	return &http.Client{Timeout: 18 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("non-HTTPS redirect")
		}
		return nil
	}}
}
func parseOfficial(body []byte) (*officialRelease, error) {
	var result struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Digest             string `json:"digest"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(result.TagName, "v") || !verRE.MatchString(result.TagName[1:]) {
		return nil, errors.New("invalid official release version")
	}
	version := result.TagName[1:]
	name := "Blood.on.the.Clocktower.Online_" + version + "_x64-setup.exe"
	urlExpected := "https://github.com/ThePandemoniumInstitute/botc-release/releases/download/" + result.TagName + "/" + name
	for _, a := range result.Assets {
		if a.Name == name {
			if a.BrowserDownloadURL != urlExpected {
				return nil, errors.New("unexpected official download URL")
			}
			if len(a.Digest) != 71 || !strings.HasPrefix(a.Digest, "sha256:") || !shaRE.MatchString(a.Digest[7:]) {
				return nil, errors.New("official release lacks a valid SHA-256 digest")
			}
			if a.Size < 1<<20 || a.Size > 250<<20 {
				return nil, errors.New("official installer size invalid")
			}
			return &officialRelease{Version: version, URL: urlExpected, SHA256: strings.ToLower(a.Digest[7:]), Size: a.Size}, nil
		}
	}
	return nil, errors.New("no official Windows x64 installer")
}
func getOfficial(ctx context.Context, client *http.Client) (*officialRelease, error) {
	b, err := readBounded(ctx, client, officialAPI, 1<<20)
	if err != nil {
		return nil, err
	}
	return parseOfficial(b)
}
func checkPublicSignature(message, sig []byte) error {
	var key struct {
		Modulus  string `xml:"Modulus"`
		Exponent string `xml:"Exponent"`
	}
	if err := xml.Unmarshal([]byte(updatePublicKeyXML), &key); err != nil {
		return err
	}
	nb, err := base64.StdEncoding.DecodeString(key.Modulus)
	if err != nil {
		return err
	}
	eb, err := base64.StdEncoding.DecodeString(key.Exponent)
	if err != nil {
		return err
	}
	exponent := int(new(big.Int).SetBytes(eb).Int64())
	if exponent < 3 || exponent > 65537 {
		return errors.New("invalid signing exponent")
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: exponent}
	h := sha256.Sum256(message)
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig)
}

// Signed feed data is only parsed after verification; no unsigned version is displayed as trusted.
func getSigned(ctx context.Context, client *http.Client) (*signedRelease, error) {
	b, err := readBounded(ctx, client, signedUpdateURL+"manifest.json", 65536)
	if err != nil {
		return nil, err
	}
	sigRaw, err := readBounded(ctx, client, signedUpdateURL+"manifest.sig", 4096)
	if err != nil {
		return nil, err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil {
		return nil, err
	}
	if err := checkPublicSignature(b, sig); err != nil {
		return nil, fmt.Errorf("invalid Grim Poster update signature: %w", err)
	}
	var r signedRelease
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.FormatVersion != 1 || !verRE.MatchString(r.ModVersion) || !verRE.MatchString(r.AppVersion) || !shaRE.MatchString(r.SHA256) || r.PackageBytes <= 0 || r.PackageBytes > 100<<20 {
		return nil, errors.New("signed manifest contains invalid release data")
	}
	if err := validateSignedAsset(r.Asset); err != nil {
		return nil, err
	}
	return &r, nil
}

// Manifest assets are relative paths, not HTTP request URIs. ParseRequestURI
// wrongly rejects safe paths such as "releases/BOTC-Grim-Poster-0.1.49.zip".
// Enforce one canonical release filename and reject encoded separators, URLs,
// traversal, query strings and fragments before combining with the fixed host.
var signedAssetRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.zip$`)

func validateSignedAsset(asset string) error {
	if !strings.HasPrefix(asset, "releases/") || strings.Count(asset, "/") != 1 ||
		strings.ContainsAny(asset, "\\%?#") || strings.Contains(asset, "..") ||
		!signedAssetRE.MatchString(strings.TrimPrefix(asset, "releases/")) {
		return errors.New("signed release path is not permitted")
	}
	u, err := url.Parse(asset)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" ||
		u.RawQuery != "" || u.Fragment != "" || u.Path != asset {
		return errors.New("invalid signed release path")
	}
	return nil
}

func makeRemote(r *signedRelease, installed *installedBOTC, p patchManifest) *remoteStatus {
	out := &remoteStatus{Release: *r}
	if installed == nil || installed.Version != r.AppVersion {
		return out
	}
	out.Compatible = true
	out.PatchInstalled = (installed.Version == p.AppVersion && strings.EqualFold(installed.SHA256, p.ExpectedPatchedSha256))
	out.UpdateAvailable = versionCmp(r.ModVersion, p.ModVersion) > 0 || (versionCmp(r.ModVersion, p.ModVersion) == 0 && !out.PatchInstalled)
	// A signed release older than our embedded patch is never offered as a downgrade.
	return out
}
func readPatch(desktop string) (patchManifest, error) {
	var p patchManifest
	b, err := os.ReadFile(filepath.Join(desktop, "patch", "patch-manifest.json"))
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(b, &p)
	if err != nil {
		return p, err
	}
	if !verRE.MatchString(p.AppVersion) || !verRE.MatchString(p.ModVersion) ||
		!(shaRE.MatchString(p.ExpectedPatchedSha256) || (p.ExpectedPatchedSha256 == "" && p.PatchVerification == "derive-from-verified-original")) {
		// Don't leak partially parsed version information into the UI: an
		// incomplete local bundle must not outrank a valid signed release.
		return patchManifest{}, errors.New("bundled patch has incomplete verification data; use the verified signed feed")
	}
	return p, nil
}

// Calculate a target SHA only from a verified official recovery copy or an
// untouched official executable. Empty/malformed payload metadata is rejected.
func resolveLocalPatchSHA(desktop, exe string, p patchManifest) (patchManifest, error) {
	if p.PatchVerification != "derive-from-verified-original" || p.ExpectedPatchedSha256 != "" {
		return p, nil
	}
	native, payload, err := readNativePatch(desktop)
	if err != nil {
		return patchManifest{}, err
	}
	if native.AppVersion != p.AppVersion || native.ModVersion != p.ModVersion {
		return patchManifest{}, errors.New("local patch versions do not match")
	}
	target, err := deriveTargetSHA(exe, payload, native)
	if err != nil {
		return patchManifest{}, err
	}
	p.ExpectedPatchedSha256 = target
	return p, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
func isExpectedDownload(actual []byte, sha string, length int64) bool {
	return int64(len(actual)) == length && strings.EqualFold(fmt.Sprintf("%x", sha256.Sum256(actual)), sha)
}
func selectedPatchSupports(state stateSnapshot) bool {
	return state.Installed != nil && state.Patch.AppVersion == state.Installed.Version
}
func readExactHTTP(ctx context.Context, client *http.Client, url string, n int64) ([]byte, error) {
	return readBounded(ctx, client, url, n)
}
func hasPEName(path string) bool { return strings.EqualFold(filepath.Base(path), "botc-app.exe") }
func isNotEmpty(s string) bool   { return len(bytes.TrimSpace([]byte(s))) > 0 }
