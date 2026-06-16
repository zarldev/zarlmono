package profile

// Builtin returns the code-defined profile set. Profiles only carry
// persona + execution settings — every profile sees the live tool
// registry. Differentiation comes from the prompt prefix and model,
// not from gating which tools exist.
func Builtin() []Profile {
	return []Profile{
		{
			Name:          NameDefault,
			Model:         "",
			PromptPrefix:  "",
			MaxIterations: 20,
		},
		{
			Name:  NameResearcher,
			Model: "",
			PromptPrefix: "You are a research agent. Gather facts, cite sources, " +
				"avoid speculation. You have a persistent notes vault — consult it " +
				"for prior work before starting, and record durable findings there " +
				"before you finish.",
			MaxIterations: 20,
		},
		{
			Name:  NameCoder,
			Model: "",
			PromptPrefix: "You are a code assistant. You operate inside a single " +
				"workspace; all file operations are scoped to it. Read before you " +
				"edit, use grep/ls to orient, and make the smallest correct change. " +
				"Prefer edit over write for existing files. Run commands through bash " +
				"when you need to build, test, or inspect the environment.",
			MaxIterations: 20,
		},
	}
}
