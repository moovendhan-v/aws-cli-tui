// Package library embeds the built-in YAML command definitions.
package library

import "embed"

//go:embed */*.yaml
var FS embed.FS
