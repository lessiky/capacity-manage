package flow

import "testing"

func TestValidateWindowTimeAcceptsAnySecond(t *testing.T) {
	for _, v := range []string{"00:00", "09:30:15", "12:34:56", "23:59:59", "3:04:05"} {
		if err := validateWindowTime(v); err != nil {
			t.Fatalf("validateWindowTime(%q) unexpected error: %v", v, err)
		}
	}
}

func TestValidateWindowTimeRejectsInvalidValue(t *testing.T) {
	for _, v := range []string{"", "10:00:60", "24:00:00", "10:0x:00", "10:00:", "10:00-11:00", "bad"} {
		if err := validateWindowTime(v); err == nil {
			t.Fatalf("validateWindowTime(%q) expected error", v)
		}
	}
}
