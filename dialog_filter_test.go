package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestFileDialogFilterPairs(t *testing.T) {
	cases := []struct {
		desc    string
		pattern string
	}{
		{desc: "内核文件 (*.exe;*.zip)", pattern: "*.exe;*.zip"},
		{desc: "可执行程序 (*.exe)", pattern: "*.exe"},
	}
	for _, tc := range cases {
		buf := fileDialogFilter(tc.desc, tc.pattern)
		parts, ok := dialogFilterParts(buf)
		if !ok || len(parts) != 2 || parts[0] != tc.desc || parts[1] != tc.pattern {
			t.Fatalf("filter(%q, %q) parts=%#v ok=%v buf=%v", tc.desc, tc.pattern, parts, ok, buf)
		}
	}
}

// dialogFilterParts splits an OPENFILENAME filter. Each string ends with one
// NUL, and the buffer ends with one extra NUL.
func dialogFilterParts(buf []uint16) ([]string, bool) {
	if len(buf) < 2 {
		return nil, false
	}
	var parts []string
	i := 0
	for i < len(buf) {
		if buf[i] == 0 {
			return parts, i == len(buf)-1
		}
		j := i
		for j < len(buf) && buf[j] != 0 {
			j++
		}
		if j >= len(buf) {
			return parts, false
		}
		parts = append(parts, windows.UTF16ToString(buf[i:j]))
		i = j + 1
	}
	return parts, false
}
