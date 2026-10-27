package events

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const line = "1793059200.001 12 192.0.2.1 TCP_MISS/200 123 GET http://user:secret@packages.test/private?q=secret - HIER_DIRECT/192.0.2.2 text/plain\n"

func write(t *testing.T, p, body string) {
	t.Helper()
	if e := os.WriteFile(p, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func read(t *testing.T, p string, cp Checkpoint) ([]Event, Checkpoint) {
	t.Helper()
	v, n, e := Read(p, cp)
	if e != nil {
		t.Fatal(e)
	}
	return v, n
}
func TestReaderResumePartialRotationAndTruncate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	write(t, p, line+"1793059201.001 5 192.0.2.1 TCP_DENIED/403 0 CONNECT blocked.test:443")
	rows, cp := read(t, p, Checkpoint{})
	if len(rows) != 1 || !cp.Backlog {
		t.Fatalf("partial consumed: %d %+v", len(rows), cp)
	}
	if rows[0].Destination != "packages.test" || strings.Contains(fmt.Sprint(rows[0]), "secret") {
		t.Fatal("URL privacy failed")
	}
	again, cp2 := read(t, p, cp)
	if len(again) != 0 {
		t.Fatal("replayed committed row")
	}
	// An uncommitted retry keeps row IDs stable.
	retry, _ := read(t, p, Checkpoint{})
	if retry[0].ID != rows[0].ID {
		t.Fatal("retry IDs differ")
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString("\n")
	f.Close()
	completed, cp := read(t, p, cp2)
	if len(completed) != 1 || completed[0].TLSMode != "tunnel" || cp.Backlog {
		t.Fatal("partial not completed")
	}
	if e := os.Rename(p, p+".0"); e != nil {
		t.Fatal(e)
	}
	write(t, p, line+line)
	rotated, cp := read(t, p, cp)
	if len(rotated) != 2 || rotated[0].ID == rotated[1].ID {
		t.Fatal("rotation duplicated or collapsed records")
	}
	write(t, p, line) // same inode, shorter content
	truncated, cp := read(t, p, cp)
	if len(truncated) != 1 || !cp.Gap {
		t.Fatal("truncation not detected")
	}
	// Missing a tracked rotation is visible even if the active log is intact.
	_ = os.Remove(p + ".0")
	_, cp = read(t, p, cp)
	if !cp.Gap {
		t.Fatal("lost source not reported")
	}
}
func TestReaderBoundedOversizedAndMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	write(t, p, strings.Repeat("x", MaxRecord*2)+"\n"+"invalid\n"+line)
	rows, cp := read(t, p, Checkpoint{})
	if len(rows) != 1 || cp.Dropped != 2 || !cp.Gap {
		t.Fatalf("bad record handling: %d %+v", len(rows), cp)
	}
	write(t, p, strings.Repeat(line, 4000))
	rows, cp = read(t, p, Checkpoint{})
	for _, pos := range cp.Files {
		if pos.Offset > BatchBytes {
			t.Fatal("batch byte limit exceeded")
		}
	}
	if len(rows) == 0 || !cp.Backlog {
		t.Fatal("bounded backlog not reported")
	}
	var all = []Event{}
	for i := 0; i < 10; i++ {
		all = append(all, rows...)
		if !cp.Backlog {
			break
		}
		rows, cp = read(t, p, cp)
	}
	if len(all) != 4000 {
		t.Fatalf("bounded reads lost rows: %d", len(all))
	}
}
func TestParserRejectsUntrustedRecords(t *testing.T) {
	for _, s := range []string{strings.Replace(line, "1793059200.001", "NaN", 1), strings.Replace(line, "192.0.2.1", "<script>", 1), strings.Replace(line, " 12 ", " -1 ", 1), strings.Replace(line, " GET ", " <script> ", 1), strings.Replace(line, "http://user:secret@packages.test/private?q=secret", "http://bad\x00host/", 1), string([]byte{0xff})} {
		if _, ok := Parse(s); ok {
			t.Fatalf("invalid log accepted: %q", s)
		}
	}
}
func TestSameLengthRewriteDetected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	write(t, p, line)
	_, cp := read(t, p, Checkpoint{})
	write(t, p, strings.Replace(line, "packages.test", "different.net", 1))
	rows, cp := read(t, p, cp)
	if len(rows) != 1 || !cp.Gap {
		t.Fatal("copytruncate/rewrite not detected")
	}
}

func TestTemporarilyMissingFileResumesWithoutReplay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	write(t, p, line)
	_, cp := read(t, p, Checkpoint{})
	if e := os.Rename(p, p+".held"); e != nil {
		t.Fatal(e)
	}
	_, cp = read(t, p, cp)
	if e := os.Rename(p+".held", p); e != nil {
		t.Fatal(e)
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString(line)
	f.Close()
	rows, cp := read(t, p, cp)
	if len(rows) != 1 || !cp.Gap {
		t.Fatal("missing source replayed its committed prefix")
	}
}
