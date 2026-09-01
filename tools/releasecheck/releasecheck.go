// Package releasecheck validates deterministic release plans for this monorepo.
package releasecheck

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const internalModulePrefix = "github.com/zarldev/zarlmono/"

var canonicalVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?$`)

var supportedModules = map[string]struct{}{
	"examples":      {},
	"swebench-eval": {},
	"zarlcode":      {},
	"zkit":          {},
}

// Plan is the validated release plan emitted to automation.
type Plan struct {
	Version string   `json:"version"`
	Scope   string   `json:"scope"`
	Modules []string `json:"modules"`
	Tags    []string `json:"tags"`
	Pins    []Pin    `json:"pins"`
}

// Pin is an internal module version required by a selected consumer.
type Pin struct {
	Consumer string `json:"consumer"`
	Module   string `json:"module"`
	Path     string `json:"path"`
	Version  string `json:"version"`
}

// Build validates inputs and repository release metadata rooted at root.
func Build(root, version, scope, custom string) (Plan, error) {
	if err := validateVersion(version); err != nil {
		return Plan{}, err
	}
	modules, err := resolveModules(scope, custom)
	if err != nil {
		return Plan{}, err
	}
	if contains(modules, "zkit") && len(modules) > 1 {
		return Plan{}, errors.New("release zkit separately, pin consumers to the published version, then release consumers")
	}

	plan := Plan{
		Version: version,
		Scope:   scope,
		Modules: modules,
		Tags:    make([]string, 0, len(modules)),
	}
	for _, module := range modules {
		plan.Tags = append(plan.Tags, module+"/"+version)
	}
	if err := validateChangelog(filepath.Join(root, "CHANGELOG.md"), plan.Tags); err != nil {
		return Plan{}, err
	}
	plan.Pins, err = readPins(root, modules)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func validateVersion(version string) error {
	if !canonicalVersion.MatchString(version) {
		return fmt.Errorf("version must be canonical semantic version vMAJOR.MINOR.PATCH with an optional valid prerelease (got %q)", version)
	}
	return nil
}

func resolveModules(scope, custom string) ([]string, error) {
	var raw []string
	switch scope {
	case "zkit", "zarlcode", "swebench-eval", "examples":
		raw = []string{scope}
	case "custom":
		for value := range strings.SplitSeq(custom, ",") {
			if value = strings.TrimSpace(value); value != "" {
				raw = append(raw, value)
			}
		}
	default:
		return nil, fmt.Errorf("unknown release scope %q", scope)
	}
	if len(raw) == 0 {
		return nil, errors.New("custom_modules must select at least one module")
	}

	modules := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, module := range raw {
		if _, ok := supportedModules[module]; !ok {
			return nil, fmt.Errorf("unsupported module %q", module)
		}
		if _, ok := seen[module]; ok {
			continue
		}
		seen[module] = struct{}{}
		modules = append(modules, module)
	}
	return modules, nil
}

func validateChangelog(path string, tags []string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open changelog: %w", err)
	}
	defer file.Close()

	lines := make([]string, 0, 256)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read changelog: %w", err)
	}

	for _, tag := range tags {
		heading := regexp.MustCompile(`^## \[` + regexp.QuoteMeta(tag) + `\] — (\d{4}-\d{2}-\d{2})$`)
		count := 0
		for _, line := range lines {
			matches := heading.FindStringSubmatch(line)
			if matches == nil {
				continue
			}
			if _, err := time.Parse(time.DateOnly, matches[1]); err != nil {
				return fmt.Errorf("CHANGELOG.md heading for %s has invalid date %q", tag, matches[1])
			}
			count++
		}
		if count != 1 {
			return fmt.Errorf("CHANGELOG.md must contain exactly one dated heading for %s (found %d)", tag, count)
		}
	}
	return nil
}

func readPins(root string, modules []string) ([]Pin, error) {
	var pins []Pin
	for _, consumer := range modules {
		if consumer == "zkit" {
			continue
		}
		path := filepath.Join(root, consumer, "go.mod")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s go.mod: %w", consumer, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(strings.SplitN(line, "//", 2)[0])
			if len(fields) >= 3 && fields[0] == "require" {
				fields = fields[1:]
			}
			if len(fields) < 2 || !strings.HasPrefix(fields[0], internalModulePrefix) {
				continue
			}
			module := strings.TrimPrefix(fields[0], internalModulePrefix)
			if module != "zkit" && module != "zarlcode" {
				continue
			}
			pins = append(pins, Pin{
				Consumer: consumer,
				Module:   module,
				Path:     fields[0],
				Version:  fields[1],
			})
		}
	}
	sort.Slice(pins, func(i, j int) bool {
		if pins[i].Consumer == pins[j].Consumer {
			return pins[i].Path < pins[j].Path
		}
		return pins[i].Consumer < pins[j].Consumer
	})
	return pins, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
