// Package ui holds guardrails' terminal styles: the brand palette and the
// fang color scheme built from it. Commands take their colors from here and
// never define them inline.
package ui

import (
	"image/color"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
)

// The brand palette, from the design tokens in docs/assets/src/brand.css.
// Dark terminals get the tokens as they are; light terminals get the deep
// variants, which keep their contrast on a white background.
var (
	Amber     = lipgloss.Color("#f5b83d") // --amber
	AmberDeep = lipgloss.Color("#9a6a0f") // --amber-3, darkened for light terminals
	Light     = lipgloss.Color("#ffd27a") // --amber-2
	Cream     = lipgloss.Color("#f4f1e8") // --cream
	Mist      = lipgloss.Color("#a7b0c8") // --mist
	Dim       = lipgloss.Color("#6b7594") // --dim
	Ink       = lipgloss.Color("#0a0f1e") // --ink
	Well      = lipgloss.Color("#111831") // --ink-2
	Block     = lipgloss.Color("#ff7a6b") // --block
	BlockDeep = lipgloss.Color("#b3261e") // --block, darkened for light terminals
)

// ColorScheme is the fang color scheme in the brand palette. c picks the
// light or dark variant for the terminal's background.
func ColorScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	amber := c(AmberDeep, Amber)
	light := c(AmberDeep, Light)
	text := c(Ink, Mist)
	block := c(BlockDeep, Block)
	return fang.ColorScheme{
		Base:           text,
		Title:          amber,
		Description:    text,
		Codeblock:      c(Cream, Well),
		Program:        amber,
		DimmedArgument: Dim,
		Comment:        Dim,
		Flag:           light,
		FlagDefault:    Dim,
		Command:        amber,
		QuotedString:   light,
		Argument:       text,
		Help:           Dim,
		Dash:           Dim,
		ErrorHeader:    [2]color.Color{Cream, block},
		ErrorDetails:   block,
	}
}
