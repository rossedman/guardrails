package ui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestColorSchemeFollowsTheBackground(t *testing.T) {
	t.Parallel()
	dark := ColorScheme(lipgloss.LightDark(true))
	light := ColorScheme(lipgloss.LightDark(false))
	if dark.Title != Amber || light.Title != AmberDeep {
		t.Errorf("title: dark %v, light %v", dark.Title, light.Title)
	}
	if dark.Codeblock != Well || light.Codeblock != Cream {
		t.Errorf("codeblock: dark %v, light %v", dark.Codeblock, light.Codeblock)
	}
	if dark.ErrorDetails != Block || light.ErrorDetails != BlockDeep {
		t.Errorf("errors: dark %v, light %v", dark.ErrorDetails, light.ErrorDetails)
	}
	if dark.Comment != Dim || light.Comment != Dim {
		t.Errorf("comments: dark %v, light %v", dark.Comment, light.Comment)
	}
}
