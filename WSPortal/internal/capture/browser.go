package capture

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// maxSessionFileSize caps how much of a browser session file is read.
const maxSessionFileSize = 8 << 20

// browserBase is a browser user-data directory containing profile folders.
type browserBase struct {
	Browser string
	Dir     string
}

// BrowserSource discovers open browser tabs by reading the browsers' session
// files. It is best-effort: Chromium-based browsers (Chrome, Chromium, Brave,
// Edge, Vivaldi) store open navigations in plaintext-bearing session files,
// while Firefox keeps its session store in a mozlz4-compressed JSON document
// (see sessionstore-backups/recovery.jsonlz4).
type BrowserSource struct {
	// Bases are Chromium-family user-data directories to scan.
	Bases []browserBase
	// FirefoxDirs are Firefox roots that contain profile directories.
	FirefoxDirs []string
}

// Name implements Source.
func (BrowserSource) Name() string { return "browsers" }

// NewBrowserSource builds a browser source for the given home directory using
// conventional browser locations on the current OS. It returns nil when no
// home directory is known.
func NewBrowserSource(home string) Source {
	if strings.TrimSpace(home) == "" {
		return nil
	}

	var bases []browserBase
	var firefoxDirs []string
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		bases = append(bases,
			browserBase{"chrome", filepath.Join(base, "Google", "Chrome")},
			browserBase{"chromium", filepath.Join(base, "Chromium")},
			browserBase{"brave", filepath.Join(base, "BraveSoftware", "Brave-Browser")},
			browserBase{"edge", filepath.Join(base, "Microsoft Edge")},
			browserBase{"vivaldi", filepath.Join(base, "Vivaldi")},
		)
		firefoxDirs = append(firefoxDirs, filepath.Join(base, "Firefox", "Profiles"))
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		bases = append(bases,
			browserBase{"chrome", filepath.Join(base, "Google", "Chrome", "User Data")},
			browserBase{"chromium", filepath.Join(base, "Chromium", "User Data")},
			browserBase{"brave", filepath.Join(base, "BraveSoftware", "Brave-Browser", "User Data")},
			browserBase{"edge", filepath.Join(base, "Microsoft", "Edge", "User Data")},
			browserBase{"vivaldi", filepath.Join(base, "Vivaldi", "User Data")},
		)
		roaming := os.Getenv("APPDATA")
		if roaming == "" {
			roaming = filepath.Join(home, "AppData", "Roaming")
		}
		firefoxDirs = append(firefoxDirs, filepath.Join(roaming, "Mozilla", "Firefox", "Profiles"))
	default:
		base := filepath.Join(home, ".config")
		bases = append(bases,
			browserBase{"chrome", filepath.Join(base, "google-chrome")},
			browserBase{"chromium", filepath.Join(base, "chromium")},
			browserBase{"brave", filepath.Join(base, "BraveSoftware", "Brave-Browser")},
			browserBase{"edge", filepath.Join(base, "microsoft-edge")},
			browserBase{"vivaldi", filepath.Join(base, "vivaldi")},
		)
		firefoxDirs = append(firefoxDirs, filepath.Join(home, ".mozilla", "firefox"))
	}
	return BrowserSource{Bases: bases, FirefoxDirs: firefoxDirs}
}

// Observe implements Source.
func (s BrowserSource) Observe() ([]Observation, error) {
	// Chromium session files store only URLs, so their tabs are recorded in a
	// single window (window 0) without titles.
	found := map[string][]TabObservation{}
	for _, base := range s.Bases {
		for _, sessionsDir := range sessionDirs(base.Dir) {
			for _, u := range readSessionURLs(sessionsDir) {
				found[base.Browser] = appendTab(found[base.Browser], TabObservation{URL: u})
			}
		}
	}
	// Firefox's session store keeps titles and per-window grouping. Only add
	// an entry when tabs were actually found, so a machine without Firefox
	// does not get a phantom empty session.
	for _, root := range s.FirefoxDirs {
		if tabs := firefoxTabs(root); len(tabs) > 0 {
			found["firefox"] = append(found["firefox"], tabs...)
		}
	}

	var observations []Observation
	for _, browser := range sortedBrowserKeys(found) {
		observations = append(observations, Observation{
			Kind: KindBrowser,
			Name: browser,
			Tabs: found[browser],
		})
	}
	return observations, nil
}

// appendTab appends a tab unless its URL is already present.
func appendTab(list []TabObservation, tab TabObservation) []TabObservation {
	url := strings.TrimSpace(tab.URL)
	if url == "" {
		return list
	}
	for _, existing := range list {
		if existing.URL == url {
			return list
		}
	}
	tab.URL = url
	return append(list, tab)
}

// sortedBrowserKeys returns the keys of a map[string][]TabObservation in
// sorted order.
func sortedBrowserKeys(m map[string][]TabObservation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sessionDirs returns the Sessions directories under a browser user-data dir,
// covering the default and any additional profiles.
func sessionDirs(userDataDir string) []string {
	var dirs []string
	if info, err := os.Stat(filepath.Join(userDataDir, "Sessions")); err == nil && info.IsDir() {
		dirs = append(dirs, filepath.Join(userDataDir, "Sessions"))
	}
	entries, err := os.ReadDir(userDataDir)
	if err != nil {
		return dirs
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name != "Default" && !strings.HasPrefix(name, "Profile ") {
			continue
		}
		dir := filepath.Join(userDataDir, name, "Sessions")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// readSessionURLs reads the newest session file in a Sessions directory and
// extracts the page URLs it contains.
func readSessionURLs(sessionsDir string) []string {
	file := newestSessionFile(sessionsDir)
	if file == "" {
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxSessionFileSize))
	if err != nil {
		return nil
	}
	return extractSessionURLs(data)
}

// newestSessionFile picks the session file most likely to describe the open
// tabs: "Current Session" when present, otherwise the newest session file.
func newestSessionFile(dir string) string {
	current := filepath.Join(dir, "Current Session")
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		return current
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	var bestMod time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "Session_") && name != "Last Session" && name != "Current Tabs" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestMod) {
			best = filepath.Join(dir, name)
			bestMod = info.ModTime()
		}
	}
	return best
}

// extractSessionURLs scans raw session data for http(s) URLs. Chromium's
// session file is a binary format, but the navigated URLs are stored verbatim
// between binary fields, so a linear scan recovers them reliably enough for a
// capture preview. Static-asset and browser-internal URLs are filtered out.
func extractSessionURLs(data []byte) []string {
	var urls []string
	seen := map[string]bool{}

	for i := 0; i+len("http://") <= len(data); i++ {
		if !bytes.HasPrefix(data[i:], []byte("http://")) &&
			!bytes.HasPrefix(data[i:], []byte("https://")) {
			continue
		}
		end := i
		for end < len(data) && isURLByte(data[end]) {
			end++
		}
		raw := strings.TrimRight(string(data[i:end]), ".,;:!?)\"'")
		i = end
		if len(raw) < len("http://")+1 || !isPageURL(raw) {
			continue
		}
		tab := normalizeTabURL(raw)
		if seen[tab] {
			continue
		}
		seen[tab] = true
		urls = append(urls, tab)
		if len(urls) >= maxTabsPerBrowser {
			break
		}
	}
	return urls
}

// normalizeTabURL canonicalizes a URL so the same page recorded twice (for
// example with and without a trailing slash on a bare host) becomes one tab,
// and strips tracking/session parameters that should not end up in a shareable
// workspace.
func normalizeTabURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Path == "/" && u.RawQuery == "" && u.Fragment == "" {
		u.Path = ""
	}
	stripTrackingParams(u)
	return u.String()
}

// trackingParams are query parameters that carry analytics or per-session
// tokens rather than meaning. Keeping them would put opaque, session-scoped
// values into a workspace that is meant to be safe to share.
var trackingParams = map[string]bool{
	"gclid": true, "fbclid": true, "msclkid": true, "dclid": true,
	"sxsrf": true, "sca_esv": true, "fbs": true, "mstk": true,
	"gs_lcrp": true, "gs_ssp": true, "oq": true, "sourceid": true,
	"ved": true, "biw": true, "bih": true, "dpr": true, "csuir": true,
	"mtid": true, "udm": true, "ntc": true, "aep": true, "vsint": true,
	"sa": true, "cs": true, "source": true, "ei": true, "usg": true,
	"sclient": true, "ie": true,
}

// stripTrackingParams removes tracking and session parameters from a URL.
func stripTrackingParams(u *url.URL) {
	if u.RawQuery == "" {
		return
	}
	query := u.Query()
	changed := false
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || trackingParams[lower] {
			query.Del(key)
			changed = true
		}
	}
	if changed {
		u.RawQuery = query.Encode()
	}
}

// mozlz4Magic is the 8-byte header Firefox's mozlz4 session-store files begin
// with, followed by a big-endian uint32 of the uncompressed size and a zlib
// stream.
const mozlz4Magic = "mozLZ40\x00"

// errNotMozlz4 means a file did not have the mozlz4 header.
var errNotMozlz4 = errors.New("not a mozlz4 file")

// firefoxTabs reads the open tabs from every profile directory under a Firefox
// root (~/.mozilla/firefox, .../Firefox/Profiles, ...). Window indices are
// renumbered so that profiles' windows stay distinct.
func firefoxTabs(root string) []TabObservation {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var tabs []TabObservation
	nextWindow := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		got := readFirefoxProfile(filepath.Join(root, entry.Name()))
		if len(got) == 0 {
			continue
		}
		maxWindow := 0
		for _, tab := range got {
			if tab.Window > maxWindow {
				maxWindow = tab.Window
			}
		}
		for _, tab := range got {
			tab.Window += nextWindow
			tabs = append(tabs, tab)
		}
		nextWindow += maxWindow + 1
	}
	return tabs
}

// readFirefoxProfile reads the newest usable session store in a profile,
// preferring the recovery backup that Firefox keeps for the running session.
func readFirefoxProfile(profileDir string) []TabObservation {
	candidates := []string{
		filepath.Join(profileDir, "sessionstore-backups", "recovery.jsonlz4"),
		filepath.Join(profileDir, "sessionstore-backups", "recovery.baklz4"),
		filepath.Join(profileDir, "sessionstore.jsonlz4"),
	}
	for _, file := range candidates {
		data := readFileLimited(file)
		if len(data) == 0 {
			continue
		}
		plain, err := decompressMozlz4(data)
		if err != nil {
			continue
		}
		if tabs := extractFirefoxTabs(plain); len(tabs) > 0 {
			return tabs
		}
	}
	return nil
}

// readFileLimited reads at most maxSessionFileSize bytes from a file.
func readFileLimited(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSessionFileSize))
	if err != nil {
		return nil
	}
	return data
}

// decompressMozlz4 decompresses a Firefox mozlz4 blob: an 8-byte magic header,
// a big-endian uint32 of the uncompressed size, then a zlib stream.
func decompressMozlz4(data []byte) ([]byte, error) {
	if len(data) < len(mozlz4Magic)+4 || !bytes.HasPrefix(data, []byte(mozlz4Magic)) {
		return nil, errNotMozlz4
	}
	size := binary.BigEndian.Uint32(data[len(mozlz4Magic) : len(mozlz4Magic)+4])
	zr, err := zlib.NewReader(bytes.NewReader(data[len(mozlz4Magic)+4:]))
	if err != nil {
		return nil, fmt.Errorf("open mozlz4 stream: %w", err)
	}
	defer zr.Close()

	// Cap by the declared size, falling back to the file-size limit when the
	// header is implausible, so a corrupt file cannot make us allocate wildly.
	limit := int64(size)
	if limit <= 0 || limit > maxSessionFileSize {
		limit = maxSessionFileSize
	}
	out, err := io.ReadAll(io.LimitReader(zr, limit))
	if err != nil {
		return nil, fmt.Errorf("decompress mozlz4 stream: %w", err)
	}
	return out, nil
}

// firefoxEntry is one navigation entry within a Firefox tab.
type firefoxEntry struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// firefoxTab is one open tab in a Firefox session store.
type firefoxTab struct {
	Entries []firefoxEntry `json:"entries"`
	Index   int            `json:"index"`
}

// firefoxWindow is one browser window in a Firefox session store.
type firefoxWindow struct {
	Tabs []firefoxTab `json:"tabs"`
}

// extractFirefoxTabs parses a Firefox session store and returns the currently
// selected entry of every open tab — its URL and title — tagged with the window
// it belongs to. URLs are normalized and filtered like Chromium tabs.
func extractFirefoxTabs(data []byte) []TabObservation {
	var store struct {
		Windows []firefoxWindow `json:"windows"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return nil
	}

	var tabs []TabObservation
	seen := map[string]bool{}
	for wi, window := range store.Windows {
		for _, tab := range window.Tabs {
			entry := firefoxTabEntry(tab)
			raw := strings.TrimSpace(entry.URL)
			if raw == "" || !isPageURL(raw) {
				continue
			}
			tabURL := normalizeTabURL(raw)
			key := fmt.Sprintf("%d\x00%s", wi, tabURL)
			if seen[key] {
				continue
			}
			seen[key] = true
			tabs = append(tabs, TabObservation{
				URL:    tabURL,
				Title:  strings.TrimSpace(entry.Title),
				Window: wi,
			})
			if len(tabs) >= maxTabsPerBrowser {
				return tabs
			}
		}
	}
	return tabs
}

// firefoxTabEntry returns the entry selected in a tab. Firefox stores a 1-based
// index; when it is missing or out of range the last entry (the most recent
// navigation) is used.
func firefoxTabEntry(tab firefoxTab) firefoxEntry {
	if len(tab.Entries) == 0 {
		return firefoxEntry{}
	}
	if tab.Index >= 1 && tab.Index <= len(tab.Entries) {
		return tab.Entries[tab.Index-1]
	}
	return tab.Entries[len(tab.Entries)-1]
}

// isURLByte reports whether b can appear inside a URL. It stops at control
// bytes, spaces, and delimiters, which mark the end of the stored string.
func isURLByte(b byte) bool {
	if b < 0x21 || b > 0x7e {
		return false
	}
	switch b {
	case '"', '\'', '<', '>', '\\', '`', '{', '}', '|', '^', ' ':
		return false
	}
	return true
}

// isPageURL reports whether a URL is a plausible browser tab rather than a
// static asset or a browser-internal endpoint.
func isPageURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}

	// A wildcard means a stored search/omnibox template, not a real tab.
	if strings.Contains(raw, "*") {
		return false
	}

	switch strings.ToLower(filepath.Ext(u.Path)) {
	case ".js", ".mjs", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg",
		".webp", ".ico", ".woff", ".woff2", ".ttf", ".map":
		return false
	}

	switch strings.ToLower(u.Host) {
	case "clients2.google.com", "clients4.google.com",
		"accounts.google.com", "play.google.com":
		return false
	}
	return true
}
