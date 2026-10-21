package main

import "testing"

func TestAdvertiseAddr(t *testing.T) {
	for _, tc := range []struct{ addr, host, want string }{
		{":8080", "", ":8080"},
		{":8080", "wt.example.com", "wt.example.com:8080"},
		{"0.0.0.0:4443", "wt.example.com", "wt.example.com:4443"},
		{":8080", "2001:db8::1", "[2001:db8::1]:8080"},
	} {
		if got := advertiseAddr(tc.addr, tc.host); got != tc.want {
			t.Errorf("advertiseAddr(%q, %q) = %q, want %q", tc.addr, tc.host, got, tc.want)
		}
	}
}
