package main

import "testing"

func TestSingleInstanceOnlyForTheAppItself(t *testing.T) {
	for _, tc := range []struct {
		o    options
		dev  string
		want bool
	}{
		{options{}, "", true},
		{options{host: true}, "", false},
		{options{connect: "1.2.3.4"}, "", false},
		{options{}, "worker", false}, // tan dev runs old and new workers side by side
	} {
		if got := wantsSingleInstance(tc.o, tc.dev); got != tc.want {
			t.Errorf("wantsSingleInstance(%+v, %q) = %v, want %v", tc.o, tc.dev, got, tc.want)
		}
	}
}

func TestSingleInstanceKeyFollowsConfigDir(t *testing.T) {
	a, b := singleInstanceKey(`C:\Users\a\AppData\Roaming\kkdesk`), singleInstanceKey(`D:\tmp\kkdesk-b`)
	if a == b {
		t.Fatal("instances with different configuration folders share a key")
	}
	if a != singleInstanceKey(`C:\Users\a\AppData\Roaming\kkdesk`) {
		t.Fatal("key is not stable")
	}
}
