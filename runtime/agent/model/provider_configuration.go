package model

import "strings"

// NormalizeProviderModel removes only Bedrock routing prefixes. Dated model
// versions and all other provider identities remain exact.
func NormalizeProviderModel(config, id string) string {
	if config != "bedrock" && config != "bedrock-anthropic" {
		return id
	}
	for _, prefix := range []string{"us.", "eu.", "apac.", "global."} {
		if strings.HasPrefix(id, prefix) {
			return strings.TrimPrefix(id, prefix)
		}
	}
	return id
}
