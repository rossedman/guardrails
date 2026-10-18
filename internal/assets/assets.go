// Package assets embeds the files `guardrails init` writes into a repo.
package assets

import "embed"

// FS holds the config template, rules and settings snippet.
//
//go:embed guardrails.yaml settings.json rules
var FS embed.FS
