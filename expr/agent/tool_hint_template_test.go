package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolExprValidateHintTemplates(t *testing.T) {
	cases := []struct {
		name   string
		tool   *ToolExpr
		errMsg string
	}{
		{
			name: "valid call and result templates",
			tool: &ToolExpr{
				Name:               "lookup",
				CallHintTemplate:   `Lookup {{ truncate .Query 24 }}`,
				ResultHintTemplate: `Found {{ count .Result.Items }} results at {{ humanTime .Result.GeneratedAt }}`,
			},
		},
		{
			name: "invalid call template syntax",
			tool: &ToolExpr{
				Name:             "lookup",
				CallHintTemplate: `Lookup {{ .Query `,
			},
			errMsg: "invalid CallHintTemplate",
		},
		{
			name: "invalid result template syntax",
			tool: &ToolExpr{
				Name:               "lookup",
				ResultHintTemplate: `Found {{ .Result.Count `,
			},
			errMsg: "invalid ResultHintTemplate",
		},
		{
			name: "unknown template function",
			tool: &ToolExpr{
				Name:             "lookup",
				CallHintTemplate: `Lookup {{ shout .Query }}`,
			},
			errMsg: `function "shout" not defined`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tool.Validate()
			if tc.errMsg == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.errMsg)
		})
	}
}
