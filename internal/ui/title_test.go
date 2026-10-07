package ui

import "testing"

func TestCleanTitle(t *testing.T) {
	if got := cleanTitle("pre · logs\x07\x1b]0;hack\x1b\\\u009b"); got != "pre · logs]0;hack\\" {
		t.Errorf("cleanTitle = %q", got)
	}
}
