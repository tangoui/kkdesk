package main

import (
	"reflect"
	"testing"
)

func TestRedactArgs(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{[]string{"-connect", "1.2.3.4", "-password", "Secret1"}, []string{"-connect", "1.2.3.4", "-password", "***"}},
		{[]string{"--password=Secret1", "-host"}, []string{"--password=***", "-host"}},
		{[]string{"-password=Secret1"}, []string{"-password=***"}},
		{[]string{"-host", "-password"}, []string{"-host", "-password"}},
		{[]string{"-fps", "60"}, []string{"-fps", "60"}},
	} {
		if got := redactArgs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("redactArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
