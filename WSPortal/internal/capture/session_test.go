package capture

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// snssCommand frames one session command as [uint16 size][uint8 id][payload].
func snssCommand(id byte, payload []byte) []byte {
	buf := make([]byte, 3+len(payload))
	binary.LittleEndian.PutUint16(buf[0:], uint16(len(payload)+1))
	buf[2] = id
	copy(buf[3:], payload)
	return buf
}

// navCommand builds a navigation-entry command for a tab's history index.
func navCommand(tabID, index uint32, url string) []byte {
	entry := make([]byte, 4+len(url))
	binary.LittleEndian.PutUint32(entry[0:], uint32(len(url)))
	copy(entry[4:], url)

	payload := make([]byte, 12+len(entry))
	binary.LittleEndian.PutUint32(payload[0:], uint32(8+len(entry))) // pickle size header
	binary.LittleEndian.PutUint32(payload[4:], tabID)
	binary.LittleEndian.PutUint32(payload[8:], index)
	copy(payload[12:], entry)
	return snssCommand(cmdUpdateTabNavigation, payload)
}

func selectedCommand(tabID, index uint32) []byte {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint32(payload[0:], tabID)
	binary.LittleEndian.PutUint32(payload[4:], index)
	return snssCommand(cmdSetSelectedNavigationIndex, payload)
}

func closedCommand(tabID uint32) []byte {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint32(payload[0:], tabID)
	return snssCommand(cmdTabClosed, payload)
}

func snssFile(commands ...[]byte) []byte {
	out := []byte("SNSS")
	var version [4]byte
	binary.LittleEndian.PutUint32(version[:], 3)
	out = append(out, version[:]...)
	for _, c := range commands {
		out = append(out, c...)
	}
	return out
}

func TestChromiumSessionURLsOpenTabsOnly(t *testing.T) {
	data := snssFile(
		// Tab 1 has history plus a current page; only the selected entry counts.
		navCommand(1, 0, "https://example.com/old"),
		navCommand(1, 1, "https://example.com/current"),
		selectedCommand(1, 1),
		// Tab 2's URL is followed by a length-byte that the raw scan would keep.
		navCommand(2, 0, "https://example.com/two&"),
		selectedCommand(2, 0),
		// Tab 3 was closed and must not appear.
		navCommand(3, 0, "https://example.com/closed"),
		selectedCommand(3, 0),
		closedCommand(3),
	)

	urls, ok := chromiumSessionURLs(data)
	if !ok {
		t.Fatal("chromiumSessionURLs: expected ok for a valid SNSS file")
	}
	want := []string{"https://example.com/current", "https://example.com/two"}
	if !reflect.DeepEqual(urls, want) {
		t.Errorf("urls = %v, want %v", urls, want)
	}
}

func TestChromiumSessionURLsRejectsForeignData(t *testing.T) {
	if _, ok := chromiumSessionURLs([]byte("not a session file at all")); ok {
		t.Error("non-SNSS data should not be parsed")
	}
	if _, ok := chromiumSessionURLs([]byte("SNSS\x03\x00\x00\x00")); ok {
		t.Error("a header with no navigation commands should not be parsed")
	}
}

func TestExtractSessionURLsTrimsTrailingFieldBytes(t *testing.T) {
	// The same page followed by different length-prefix bytes must collapse to
	// one tab.
	data := []byte("https://example.com/page&\x00" +
		"https://example.com/page#\x00" +
		"https://example.com/page\x00")
	got := extractSessionURLs(data)
	want := []string{"https://example.com/page"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractSessionURLs = %v, want %v", got, want)
	}
}
