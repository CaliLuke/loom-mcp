package planner

func firstFailedToolOutput(outputs []*ToolOutput) *ToolOutput {
	for _, output := range outputs {
		if output == nil || output.Error == nil {
			continue
		}
		return output
	}
	return nil
}
