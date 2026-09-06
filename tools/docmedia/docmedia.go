// Package docmedia synchronizes rendered documentation GIFs to Astro's public assets.
package docmedia

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Mapping identifies one canonical rendered GIF and its public Astro copy.
type Mapping struct {
	Canonical string
	Public    string
}

// Mappings returns every canonical-to-public documentation GIF mapping.
func Mappings() []Mapping {
	return []Mapping{
		{Canonical: "zarlcode/docs/images/hero.gif", Public: "site/public/zarlcode-hero2.gif"},
		{Canonical: "zarlcode/docs/images/screen-cockpit.gif", Public: "site/public/zarlcode-cockpit.gif"},
		{Canonical: "zarlcode/docs/images/screen-fileviewer.gif", Public: "site/public/zarlcode-fileviewer.gif"},
		{Canonical: "zarlcode/docs/images/screen-modelpicker.gif", Public: "site/public/zarlcode-modelpicker.gif"},
		{Canonical: "zarlcode/docs/images/screen-planmode.gif", Public: "site/public/zarlcode-planmode.gif"},
		{Canonical: "zarlcode/docs/images/screen-subagents.gif", Public: "site/public/zarlcode-subagents.gif"},
		{Canonical: "zarlcode/docs/images/screen-workingset.gif", Public: "site/public/zarlcode-workingset.gif"},
		{Canonical: "zarlcode/docs/images/workflow-demo.gif", Public: "site/public/zarlcode-workflow-demo.gif"},
		{Canonical: "zarlcode/docs/images/onboarding-local.gif", Public: "site/public/zarlcode-onboarding-local.gif"},
		{Canonical: "zarlcode/docs/images/onboarding-provider.gif", Public: "site/public/zarlcode-onboarding-provider.gif"},
	}
}

// Sync copies every rendered canonical GIF to its Astro public destination.
func Sync(root string) error {
	for _, mapping := range Mappings() {
		canonical := filepath.Join(root, mapping.Canonical)
		body, err := os.ReadFile(canonical)
		if err != nil {
			return fmt.Errorf("read canonical %s: %w", mapping.Canonical, err)
		}
		public := filepath.Join(root, mapping.Public)
		if err := os.WriteFile(public, body, 0o644); err != nil { //nolint:gosec // Public site assets must remain readable by the web server.
			return fmt.Errorf("write public %s: %w", mapping.Public, err)
		}
	}
	return nil
}

// Check reports a missing or divergent public GIF without changing any files.
func Check(root string) error {
	for _, mapping := range Mappings() {
		canonical, err := os.ReadFile(filepath.Join(root, mapping.Canonical))
		if err != nil {
			return fmt.Errorf("read canonical %s: %w", mapping.Canonical, err)
		}
		public, err := os.ReadFile(filepath.Join(root, mapping.Public))
		if err != nil {
			return fmt.Errorf("read public %s: %w", mapping.Public, err)
		}
		if !bytes.Equal(canonical, public) {
			return fmt.Errorf("public GIF differs: %s -> %s", mapping.Canonical, mapping.Public)
		}
	}
	return nil
}
