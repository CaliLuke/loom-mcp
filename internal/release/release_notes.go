package release

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// notes reads reviewed prose from the selected source, never the publisher's
// checkout or commit subjects. Every release in a train describes changes since
// the preceding stable release; only version placeholders and the compare link
// are rendered by the publisher.
func (p publisher) notes(ctx context.Context, version, source string, tags []tag) (string, error) {
	train, _, _ := strings.Cut(version, "-alpha.")
	if err := validateTrainVersion(train); err != nil {
		return "", err
	}
	path := "docs/releases/" + train + ".md"
	authored, err := p.git(ctx, "show", source+":"+path)
	if err != nil {
		return "", fmt.Errorf("read authored release notes %s at %s: %w", path, source, err)
	}
	if len(strings.Fields(authored)) < 20 {
		return "", fmt.Errorf("%s: authored release notes must contain at least 20 words", path)
	}
	notes := strings.ReplaceAll(strings.TrimSpace(authored), "{{version}}", version) + "\n"
	base := ""
	for _, t := range tags {
		if semver.Prerelease(t.version) != "" || semver.Compare(t.version, train) >= 0 {
			continue
		}
		if base == "" || semver.Compare(t.version, base) > 0 {
			base = t.version
		}
	}
	if base != "" {
		notes += fmt.Sprintf("\n[Full changelog](https://github.com/%s/compare/%s...%s)\n", repository, base, version)
	}
	return notes, nil
}
