package capture

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// Chromium session (SNSS) command ids used to reconstruct the open tabs.
const (
	// cmdUpdateTabNavigation stores one navigation entry for a tab:
	// [pickle size u32][tab id u32][index u32][serialized navigation].
	cmdUpdateTabNavigation = 6
	// cmdSetSelectedNavigationIndex selects the current entry of a tab:
	// [tab id u32][index u32].
	cmdSetSelectedNavigationIndex = 7
	// cmdTabClosed marks a tab closed: [tab id u32][...].
	cmdTabClosed = 8
)

// chromiumSessionURLs extracts the URLs of the currently open tabs from a
// Chromium session (SNSS) file.
//
// A session file is a command stream: an "SNSS" magic and version, then records
// of [uint16 size][uint8 command][size-1 payload]. Navigation entries are
// command 6, the selected index is command 7, and closed tabs are command 8.
// Reconstructing the open tabs this way yields exactly one URL per open tab,
// including a tab's current (possibly historical) page rather than its whole
// back/forward history.
//
// It reports ok=false when the data is not in the expected format, so the
// caller can fall back to the simpler scanner.
func chromiumSessionURLs(data []byte) (urls []string, ok bool) {
	if len(data) < 8 || !bytes.Equal(data[:4], []byte("SNSS")) {
		return nil, false
	}

	tabs := map[uint32]*chromiumTab{}
	var order []uint32
	get := func(id uint32) *chromiumTab {
		t := tabs[id]
		if t == nil {
			t = &chromiumTab{entries: map[uint32]string{}}
			tabs[id] = t
			order = append(order, id)
		}
		return t
	}

	off := 8
	sawNavigation := false
	for off+3 <= len(data) {
		size := int(binary.LittleEndian.Uint16(data[off:]))
		if size < 1 || off+3+(size-1) > len(data) {
			break
		}
		cmd := data[off+2]
		payload := data[off+3 : off+3+(size-1)]

		switch cmd {
		case cmdUpdateTabNavigation:
			// The payload begins with the pickle's own size header, so the
			// tab id and index sit at offsets 4 and 8.
			if len(payload) >= 12 {
				if u := chromiumEntryURL(payload[12:]); u != "" {
					t := get(binary.LittleEndian.Uint32(payload[4:]))
					t.entries[binary.LittleEndian.Uint32(payload[8:])] = u
					sawNavigation = true
				}
			}
		case cmdSetSelectedNavigationIndex:
			if len(payload) >= 8 {
				t := get(binary.LittleEndian.Uint32(payload[0:]))
				t.hasSel = true
				t.sel = binary.LittleEndian.Uint32(payload[4:])
			}
		case cmdTabClosed:
			if len(payload) >= 4 {
				get(binary.LittleEndian.Uint32(payload[0:])).closed = true
			}
		}

		off += 3 + (size - 1)
	}

	if !sawNavigation {
		return nil, false
	}

	seen := map[string]bool{}
	for _, id := range order {
		t := tabs[id]
		if t == nil || t.closed || len(t.entries) == 0 {
			continue
		}
		url := t.currentURL()
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		urls = append(urls, url)
		if len(urls) >= maxTabsPerBrowser {
			break
		}
	}
	return urls, true
}

// chromiumTab is the state reconstructed for one tab while parsing a session
// file.
type chromiumTab struct {
	entries map[uint32]string
	sel     uint32
	hasSel  bool
	closed  bool
}

// currentURL returns the selected navigation entry, falling back to the most
// recent entry when the selected index is unknown.
func (t *chromiumTab) currentURL() string {
	if t.hasSel {
		if u, ok := t.entries[t.sel]; ok {
			return u
		}
	}
	var best uint32
	first := true
	for index := range t.entries {
		if first || index > best {
			best = index
			first = false
		}
	}
	return t.entries[best]
}

// chromiumEntryURL recovers the URL from a serialized navigation entry. The URL
// is a pickle string (a uint32 length followed by its bytes), so the length is
// used when it validates; otherwise the URL is scanned to its end. Either way
// the result is validated and normalized.
func chromiumEntryURL(entry []byte) string {
	for i := 0; i+len("http://") <= len(entry); i++ {
		if !bytes.HasPrefix(entry[i:], []byte("http://")) &&
			!bytes.HasPrefix(entry[i:], []byte("https://")) {
			continue
		}
		if i >= 4 {
			n := int(int32(binary.LittleEndian.Uint32(entry[i-4:])))
			if n > 0 && i+n <= len(entry) {
				if s := plainASCII(entry[i : i+n]); s != "" {
					if u := acceptedURL(s); u != "" {
						return u
					}
				}
			}
		}
		end := i
		for end < len(entry) && isURLByte(entry[end]) {
			end++
		}
		if u := acceptedURL(string(entry[i:end])); u != "" {
			return u
		}
	}
	return ""
}

// plainASCII returns s when every byte is printable ASCII, else "".
func plainASCII(b []byte) string {
	for _, c := range b {
		if c < 0x21 || c > 0x7e {
			return ""
		}
	}
	return string(b)
}

// acceptedURL trims a candidate's trailing field bytes, rejects non-page URLs
// (static assets, browser internals, search templates), and normalizes the
// result. Trailing length-prefix bytes from the surrounding binary are the main
// source of near-duplicate tabs, so they are trimmed aggressively.
func acceptedURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), ".,;:!?)\"'&#=+")
	if raw == "" || !isPageURL(raw) {
		return ""
	}
	return normalizeTabURL(raw)
}
