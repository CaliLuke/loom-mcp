package codegen

type (
	cliServiceTemplateData struct {
		Name    string
		Alias   string
		Methods []cliMethodTemplateData
	}

	cliMethodTemplateData struct {
		Command  string
		Endpoint string
	}
)
