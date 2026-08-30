package providers

func init() { Register(oekaki) }

// oekaki claims the types the tool itself synthesises when an overlay
// names something no parser produced.
//
// They are real nodes and they need a real category, because "what is a log
// sink" is classification and classification lives here. But their provider is
// the tool, which is the point: on the provider axis they group under a
// heading that reads honestly as the things oekaki invented because
// somebody asserted them, rather than blending in with resources that were
// actually found in the input.
//
// A log destination that exists in the IaC is an ordinary resource and keeps
// its own type. This profile covers only the ones that had to be invented.
var oekaki = &Profile{
	Name:     "oekaki",
	Prefixes: []string{"oekaki_"},

	Categories: map[string]Category{
		"oekaki_log_sink": Storage,
		"oekaki_asserted": Generic,
	},
}
