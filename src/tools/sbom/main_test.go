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
	dir := t.TempDir()
	df := write(t, "Dockerfile", "FROM alpine\n# renovate: datasource=github-releases\nARG SQUID_TAG=SQUID_7_7\n")
	out := filepath.Join(dir, "sbom.json")
	if e := run(write(t, "image.json", imageBOM), write(t, "web.json", webBOM), df, out); e != nil {
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

func TestSquidVersionMustBeSingleAndNumeric(t *testing.T) {
	for _, body := range []string{
		"FROM alpine\n",
		"ARG SQUID_TAG=SQUID_7_7\nARG SQUID_TAG=SQUID_7_8\n",
		"ARG SQUID_TAG=latest\n",
		"ARG SQUID_VERSION=7.7\n",
	} {
		if v, e := squidVersion(write(t, "Dockerfile", body)); e == nil {
			t.Fatalf("%q: want error, got %s", body, v)
		}
	}
}

func TestRejectsNonCycloneDX(t *testing.T) {
	df := write(t, "Dockerfile", "ARG SQUID_TAG=SQUID_7_7\n")
	spdx := write(t, "spdx.json", `{"spdxVersion":"SPDX-2.3"}`)
	if e := run(spdx, write(t, "web.json", webBOM), df, filepath.Join(t.TempDir(), "o.json")); e == nil {
		t.Fatal("SPDX accepted as CycloneDX")
	}
}

func TestRealDockerfileHasSquidVersion(t *testing.T) {
	const path = "../../../deploy/squid/Dockerfile"
	if _, e := os.Stat(path); errors.Is(e, os.ErrNotExist) {
		t.Skip("deploy/squid/Dockerfile is not in this checkout")
	}
	if _, e := squidVersion(path); e != nil {
		t.Fatal(e)
	}
}
