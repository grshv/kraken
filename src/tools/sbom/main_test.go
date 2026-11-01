package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if e := os.WriteFile(p, []byte(body), 0o644); e != nil {
		t.Fatal(e)
	}
	return p
}

const imageBOM = `{"bomFormat":"CycloneDX","specVersion":"1.6",
 "components":[{"bom-ref":"apk-1","name":"musl"},{"bom-ref":"go-1","name":"golang.org/x/sys"}],
 "dependencies":[{"ref":"apk-1"}]}`

const webBOM = `{"bomFormat":"CycloneDX","specVersion":"1.6",
 "components":[{"bom-ref":"npm-1","name":"react"},{"bom-ref":"go-1","name":"golang.org/x/sys"}],
 "dependencies":[{"ref":"npm-1"},{"ref":"apk-1"}]}`

func TestMergeAddsWebAndSquidOnce(t *testing.T) {
	out := filepath.Join(t.TempDir(), "sbom.json")
	if e := run(write(t, "image.json", imageBOM), write(t, "web.json", webBOM), "7.7", out); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(out)
	var v struct {
		Components []struct {
			Ref  string `json:"bom-ref"`
			Name string `json:"name"`
			CPE  string `json:"cpe"`
		} `json:"components"`
		Dependencies []struct {
			Ref string `json:"ref"`
		} `json:"dependencies"`
	}
	if e := json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	var names []string
	cpe := ""
	for _, c := range v.Components {
		names = append(names, c.Name)
		if c.Name == "squid" {
			cpe = c.CPE
		}
	}
	if got := strings.Join(names, ","); got != "musl,golang.org/x/sys,react,squid" {
		t.Fatalf("components %s", got)
	}
	if cpe != "cpe:2.3:a:squid-cache:squid:7.7:*:*:*:*:*:*:*" {
		t.Fatalf("squid cpe %q", cpe)
	}
	if len(v.Dependencies) != 2 {
		t.Fatalf("dependencies %+v", v.Dependencies)
	}
}

func TestSquidVersionMustBeNumeric(t *testing.T) {
	for _, v := range []string{"", "7", "latest", "SQUID_7_7", "7.7\n", "7.7-rc1", "v7.7", "7..7"} {
		out := filepath.Join(t.TempDir(), "o.json")
		if e := run(write(t, "image.json", imageBOM), write(t, "web.json", webBOM), v, out); e == nil {
			t.Fatalf("%q accepted as a Squid version", v)
		}
		if _, e := os.Stat(out); !errors.Is(e, os.ErrNotExist) {
			t.Fatalf("%q: output written for a rejected version", v)
		}
	}
	if e := run(write(t, "image.json", imageBOM), write(t, "web.json", webBOM), "7.8.1", filepath.Join(t.TempDir(), "o.json")); e != nil {
		t.Fatalf("7.8.1 rejected: %v", e)
	}
}

func TestRejectsNonCycloneDX(t *testing.T) {
	spdx := write(t, "spdx.json", `{"spdxVersion":"SPDX-2.3"}`)
	if e := run(spdx, write(t, "web.json", webBOM), "7.7", filepath.Join(t.TempDir(), "o.json")); e == nil {
		t.Fatal("SPDX accepted as CycloneDX")
	}
}
