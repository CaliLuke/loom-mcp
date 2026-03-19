package assistantapi

import (
	"fmt"

	assistant "example.com/assistant/gen/assistant"
)

const figmaDesignSystemURI = "figma://design-system/mobile-checkout"

// FixtureDesignSystem returns the canonical fake Figma design system used by
// the MCP integration fixture.
func FixtureDesignSystem() *assistant.DesignSystem {
	return &assistant.DesignSystem{
		Name:     "Mobile Commerce System",
		Version:  "2026.03",
		Platform: "ios",
		Tokens: &assistant.DesignTokenGroup{
			Colors:     []string{"accent.brand=#1ABCFE", "surface.canvas=#0B1220", "text.primary=#F8FAFC"},
			Spacing:    []string{"space.2=8", "space.4=16", "space.6=24"},
			Typography: []string{"title.lg=SF Pro Display/28/700", "body.md=SF Pro Text/16/400"},
		},
	}
}

// FixtureDPISpec returns the canonical implementation plan derived from the
// fake Figma fixture payload.
func FixtureDPISpec(p *assistant.GenerateDpiSpecPayload) *assistant.DPISpec {
	width := 1440
	height := 1024
	if p != nil && p.Platform == "ios" {
		width = 390
		height = 844
	}

	var sections []*assistant.DPISection
	if p != nil {
		for _, sectionName := range p.Sections {
			sections = append(sections, &assistant.DPISection{
				Name:      sectionName,
				Component: fixtureSectionComponent(sectionName),
				Notes: []string{
					"Respect the Figma spacing tokens for this section.",
					"Preserve the section order from the source frame.",
				},
			})
		}
	}

	spec := &assistant.DPISpec{
		Viewport:        &assistant.DPIViewport{Width: width, Height: height},
		Sections:        sections,
		PrimaryCta:      &assistant.DPICallToAction{Style: "brand-filled"},
		DesignTokensURI: figmaDesignSystemURI,
	}
	if p != nil {
		spec.ScreenTitle = p.ScreenTitle
		spec.Platform = p.Platform
		spec.Density = p.Density
		spec.PrimaryCta.Label = p.PrimaryCta
		if p.IncludeDevNotes == nil || *p.IncludeDevNotes {
			spec.DevNotes = []string{
				"Use the design system resource before writing implementation code.",
				"Keep CTA prominence aligned with the brand-filled token set.",
			}
		}
	}
	return spec
}

// FixtureImplementationPrompt returns the canonical implementation prompt text
// for the fake Figma fixture.
func FixtureImplementationPrompt(screenTitle string, framework string, designTokensURI string, spec *assistant.DPISpec) string {
	if spec == nil {
		spec = &assistant.DPISpec{}
	}
	if screenTitle == "" {
		screenTitle = spec.ScreenTitle
	}
	if designTokensURI == "" {
		designTokensURI = spec.DesignTokensURI
	}

	sectionCount := len(spec.Sections)
	viewportWidth := 0
	viewportHeight := 0
	ctaLabel := ""
	if spec.Viewport != nil {
		viewportWidth = spec.Viewport.Width
		viewportHeight = spec.Viewport.Height
	}
	if spec.PrimaryCta != nil {
		ctaLabel = spec.PrimaryCta.Label
	}
	return fmt.Sprintf(
		"Implement %s in %s using %s. Preserve the %d ordered sections, viewport %dx%d, and CTA label %q.",
		screenTitle,
		framework,
		designTokensURI,
		sectionCount,
		viewportWidth,
		viewportHeight,
		ctaLabel,
	)
}

func fixtureSectionComponent(name string) string {
	switch name {
	case "hero":
		return "HeroCard"
	case "summary":
		return "OrderSummary"
	case "payment_form":
		return "PaymentForm"
	case "trust_bar":
		return "TrustBar"
	default:
		return "ContentBlock"
	}
}
