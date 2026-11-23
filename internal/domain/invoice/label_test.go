package invoice

import "testing"

func TestDefaultLabelWritesTheMonthAndTheRoom(t *testing.T) {
	for _, tc := range []struct {
		month  int
		room   string
		number int
		want   string
	}{
		{11, "P101", 7, "HD T11-P101"},
		{4, "v-081", 1, "HD T04-V-081"},
		// Accents and spaces go, the rest is cut to 14 characters in all.
		{1, "Phòng 101", 1, "HD T01-PHONG10"},
		{12, "Tầng 2 / Phòng A", 1, "HD T12-TANG2/P"},
		{3, "Đ", 1, "HD T03-D"},
		{3, "đường Ơ", 1, "HD T03-DUONGO"},
		// A name with nothing usable falls back to the number of the bill in its month.
		{11, "★★", 7, "HD T11-007"},
		{11, "", 12, "HD T11-012"},
	} {
		if got := DefaultLabel(tc.month, tc.room, tc.number); got != tc.want {
			t.Errorf("DefaultLabel(%d, %q, %d) = %q, want %q", tc.month, tc.room, tc.number, got, tc.want)
		}
	}
	long := DefaultLabel(12, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", 1)
	if len(long) != MaxLabelLength {
		t.Errorf("a long name is cut to %d characters, got %q", MaxLabelLength, long)
	}
}

func TestEveryDefaultLabelIsValid(t *testing.T) {
	for _, room := range []string{"P101", "Phòng 101", "★★", "", "A/B-1.2_3", "Tầng 2 / Phòng A"} {
		if label := DefaultLabel(11, room, 3); !ValidLabel(label) {
			t.Errorf("%q gives the invalid label %q", room, label)
		}
	}
}

func TestValidLabel(t *testing.T) {
	for label, want := range map[string]bool{
		"P101 T11": true, "P102-T11": true, "A": true, "a.b_c/d-e": true, "HD T11-P101": true,
		"": false, "Tiền phòng": false, "AAAAAAAAAAAAAAA": false, "AAAAAAAAAAAAAA": true, "a;b": false, "a@b": false,
	} {
		if got := ValidLabel(label); got != want {
			t.Errorf("ValidLabel(%q) = %v, want %v", label, got, want)
		}
	}
}

func TestComposeCodeAndTailOf(t *testing.T) {
	code := ComposeCode("P101 T11", "AB2CD")
	if code != "P101 T11 AB2CD" || len(code) > MaxCodeLength {
		t.Fatalf("code: %q", code)
	}
	if tail, ok := TailOf(code); !ok || tail != "AB2CD" {
		t.Errorf("tail: %q %v", tail, ok)
	}
	for _, bad := range []string{"P101", "P101 T11", "P101 AB2C", "P101 AB2CDE", "P101 ab2cd", "P101 AB0CD"} {
		if tail, ok := TailOf(bad); ok {
			t.Errorf("%q has no tail, got %q", bad, tail)
		}
	}
}
