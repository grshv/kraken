// Command sbom merges the CycloneDX SBOMs of the release into one document
// that a vulnerability scanner checks as a whole:
//
//   - the final image (Alpine packages, Go modules of kraken and pebble);
//   - the web bundle (production npm dependencies compiled into kraken, which
//     a scan of the image cannot see);
//   - Squid, built from upstream sources without package metadata, added as a
//     component with its CPE so scanners match it against NVD.
//
// The Squid version is read from ARG SQUID_TAG (SQUID_<major>_<minor>) in
// deploy/squid/Dockerfile, so the SBOM cannot drift from what the image builds.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	image := flag.String("image", "", "CycloneDX JSON of the final image")
	web := flag.String("web", "", "CycloneDX JSON of the web dependencies")
	dockerfile := flag.String("dockerfile", "", "Squid Dockerfile with ARG SQUID_TAG")
	out := flag.String("out", "", "merged CycloneDX JSON")
	flag.Parse()
	if *image == "" || *web == "" || *dockerfile == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if e := run(*image, *web, *dockerfile, *out); e != nil {
		fmt.Fprintln(os.Stderr, "sbom:", e)
		os.Exit(1)
	}
}

func run(imagePath, webPath, dockerfilePath, outPath string) error {
	image, e := readBOM(imagePath)
	if e != nil {
		return e
	}
	web, e := readBOM(webPath)
	if e != nil {
		return e
	}
	version, e := squidVersion(dockerfilePath)
	if e != nil {
		return e
	}
	merged, e := merge(image, web, version)
	if e != nil {
		return e
	}
	b, e := json.MarshalIndent(merged, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(outPath, append(b, '\n'), 0o644)
}

type bom = map[string]any

func readBOM(path string) (bom, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var v bom
	if e := json.Unmarshal(b, &v); e != nil {
		return nil, fmt.Errorf("%s: %w", path, e)
	}
	if v["bomFormat"] != "CycloneDX" {
		return nil, fmt.Errorf("%s: not a CycloneDX document", path)
	}
	return v, nil
}

var argSquid = regexp.MustCompile(`^ARG\s+SQUID_TAG=SQUID_([0-9]+(?:_[0-9]+)+)\s*$`)

// squidVersion returns the version of the single ARG SQUID_TAG of the
// Dockerfile: SQUID_7_7 is 7.7.
func squidVersion(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	var found []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := argSquid.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			found = append(found, strings.ReplaceAll(m[1], "_", "."))
		}
	}
	if e := sc.Err(); e != nil {
		return "", e
	}
	if len(found) != 1 {
		return "", fmt.Errorf("%s: want one ARG SQUID_TAG=SQUID_<x>_<y>, found %d", path, len(found))
	}
	return found[0], nil
}

// merge returns the image SBOM with the web components and the Squid
// component appended; components and dependencies are deduplicated by
// bom-ref, so merging twice changes nothing.
func merge(image, web bom, squid string) (bom, error) {
	components, ok := list(image, "components")
	if !ok {
		return nil, errors.New("image SBOM has no components")
	}
	webComponents, _ := list(web, "components")
	seen := map[string]bool{}
	var all []any
	add := func(items []any) {
		for _, c := range items {
			ref, _ := c.(map[string]any)["bom-ref"].(string)
			if ref != "" && seen[ref] {
				continue
			}
			if ref != "" {
				seen[ref] = true
			}
			all = append(all, c)
		}
	}
	add(components)
	add(webComponents)
	add([]any{squidComponent(squid)})
	image["components"] = all

	deps, _ := list(image, "dependencies")
	webDeps, _ := list(web, "dependencies")
	seenDep := map[string]bool{}
	var allDeps []any
	for _, d := range append(deps, webDeps...) {
		ref, _ := d.(map[string]any)["ref"].(string)
		if ref != "" && seenDep[ref] {
			continue
		}
		seenDep[ref] = true
		allDeps = append(allDeps, d)
	}
	if allDeps != nil {
		image["dependencies"] = allDeps
	}
	return image, nil
}

func list(v bom, key string) ([]any, bool) {
	l, ok := v[key].([]any)
	return l, ok
}

func squidComponent(version string) map[string]any {
	return map[string]any{
		"bom-ref":     "pkg:generic/squid-cache/squid@" + version,
		"type":        "application",
		"name":        "squid",
		"version":     version,
		"description": "Squid built from the upstream release in deploy/Dockerfile",
		"purl":        "pkg:generic/squid-cache/squid@" + version,
		"cpe":         "cpe:2.3:a:squid-cache:squid:" + version + ":*:*:*:*:*:*:*",
	}
}
