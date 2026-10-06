package api

import (
	"os"
	"regexp"
	"testing"
)

// The version this program declares is that of the description it was
// built against.  Where the server's repository sits beside this one,
// as it does in development, the two are compared, so that a change to
// the API is followed here and the number bumped with it.
func TestSpecVersionMatchesTheDescription(t *testing.T) {
	data, err := os.ReadFile("../../../test.streamingchasers.com/docs/openapi.yaml")
	if err != nil {
		t.Skip("the server's OpenAPI description is not beside this repository")
	}
	match := regexp.MustCompile(`(?m)^  version: *"?([0-9][^"\s]*)"?`).FindSubmatch(data)
	if match == nil {
		t.Fatal("the description has no info.version")
	}
	if got := string(match[1]); got != SpecVersion {
		t.Errorf("the description is version %s and SpecVersion is %s; follow what changed in the API, then bump SpecVersion", got, SpecVersion)
	}
}

func TestVersionsRemembersWhatTheServerSaid(t *testing.T) {
	var v Versions
	v.Seen("", "ahead")
	if v.Server() != "" || v.Status() != "" {
		t.Error("an answer without a version should change nothing")
	}
	v.Seen("1.1.0", "current")
	v.Seen("1.1.0", "")
	if v.Server() != "1.1.0" || v.Status() != "current" {
		t.Errorf("got %q %q", v.Server(), v.Status())
	}
	var none *Versions
	none.Seen("1", "current")
	if none.Server() != "" || none.Status() != "" {
		t.Error("a nil Versions should be inert")
	}
}
