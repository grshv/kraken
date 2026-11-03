// Command sbom merges the CycloneDX SBOMs of the release into one document
// that a vulnerability scanner checks as a whole:
//
//   - the final image (Alpine packages, Go modules of kraken and pebble);
//   - the web bundle (production npm dependencies compiled into kraken, which
//     a scan of the image cannot see);
//   - Squid, built from upstream sources without package metadata, added as a
//     component with its CPE so scanners match it against NVD.
//
// The Squid version comes from `squid -v` of the scanned image itself (see
// scan-sbom in the Makefile), so the SBOM cannot drift from what the image
// actually carries, whichever Squid image it was built with.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
)

func main() {
	image := flag.String("image", "", "CycloneDX JSON of the final image")
	web := flag.String("web", "", "CycloneDX JSON of the web dependencies")
	squid := flag.String("squid-version", "", "Squid version reported by squid -v in the image, e.g. 7.7")
	out := flag.String("out", "", "merged CycloneDX JSON")
	flag.Parse()
	if *image == "" || *web == "" || *squid == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if e := run(*image, *web, *squid, *out); e != nil {
		fmt.Fprintln(os.Stderr, "sbom:", e)
		os.Exit(1)
	}
}

var squidVersionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+$`)

func run(imagePath, webPath, squidVersion, outPath string) error {
	if !squidVersionPattern.MatchString(squidVersion) {
		return fmt.Errorf("squid version %q: want <major>.<minor>[.<patch>]", squidVersion)
	}
	image, e := readBOM(imagePath)
	if e != nil {
		return e
	}
	web, e := readBOM(webPath)
	if e != nil {
		return e
	}
	merged, e := merge(image, web, squidVersion)
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
		"description": "Squid built from the upstream release in deploy/squid",
		"purl":        "pkg:generic/squid-cache/squid@" + version,
		"cpe":         "cpe:2.3:a:squid-cache:squid:" + version + ":*:*:*:*:*:*:*",
	}
}
