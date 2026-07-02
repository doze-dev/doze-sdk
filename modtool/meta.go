package modtool

import "github.com/doze-dev/doze-sdk/engine"

// metaFile is the meta.yaml shape the registry site and `doze modules docs`
// consume. It intentionally has NO versions field: engine support is
// machine-readable in the signed index (releases.<v>.engines), not docs
// metadata. Field names match what the site's ArgTable reads.
type metaFile struct {
	Title        string     `yaml:"title"`
	Tagline      string     `yaml:"tagline"`
	Category     string     `yaml:"category"`
	Engine       string     `yaml:"engine"`
	Port         int        `yaml:"port,omitempty"`
	Example      string     `yaml:"example,omitempty"`
	ExampleLabel string     `yaml:"exampleLabel,omitempty"`
	Description  string     `yaml:"description,omitempty"`
	Homepage     string     `yaml:"homepage,omitempty"`
	Source       string     `yaml:"source,omitempty"`
	Config       metaConfig `yaml:"config"`
}

type metaConfig struct {
	Arguments []metaArg   `yaml:"arguments"`
	Blocks    []metaBlock `yaml:"blocks,omitempty"`
}

type metaBlock struct {
	Name      string    `yaml:"name"`
	Label     string    `yaml:"label,omitempty"`
	Desc      string    `yaml:"desc,omitempty"`
	Arguments []metaArg `yaml:"arguments"`
}

type metaArg struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Default  string `yaml:"default,omitempty"`
	Desc     string `yaml:"desc,omitempty"`
	Required bool   `yaml:"required,omitempty"`
	Since    string `yaml:"since,omitempty"` // engine major that introduced the argument
	Until    string `yaml:"until,omitempty"` // engine major that removed it
}

func toMetaFile(name string, d engine.Description) metaFile {
	blocks := make([]metaBlock, 0, len(d.Blocks))
	for _, b := range d.Blocks {
		blocks = append(blocks, metaBlock{Name: b.Name, Label: b.Label, Desc: b.Desc, Arguments: toMetaArgs(b.Args)})
	}
	return metaFile{
		Title: d.Title, Tagline: d.Tagline, Category: d.Category, Engine: name,
		Port: d.Port, Example: d.Example, ExampleLabel: d.ExampleLabel,
		Description: d.Description, Homepage: d.Homepage, Source: d.Source,
		Config: metaConfig{Arguments: toMetaArgs(d.Config), Blocks: blocks},
	}
}

func toMetaArgs(in []engine.ConfigArg) []metaArg {
	out := make([]metaArg, 0, len(in))
	for _, a := range in {
		out = append(out, metaArg{
			Name: a.Name, Type: a.Type, Default: a.Default, Desc: a.Desc,
			Required: a.Required, Since: a.Since, Until: a.Until,
		})
	}
	return out
}
