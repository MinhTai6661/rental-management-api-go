package identityhttp

import "testing"

func TestBlankToNil(t *testing.T) {
	blank, text := "   ", " 0901 "
	if blankToNil(nil) != nil || blankToNil(&blank) != nil {
		t.Error("nothing or blank means not given")
	}
	if got := blankToNil(&text); got == nil || *got != "0901" {
		t.Errorf("got %v", got)
	}
}
