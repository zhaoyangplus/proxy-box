package launcher

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type shellCall struct {
	n      int
	target string
	show   int
	code   uintptr
	err    error
}

func trapShell(t *testing.T) *shellCall {
	t.Helper()
	call := &shellCall{code: 42}
	prev := shellOpenFn
	shellOpenFn = func(target string, showCmd int) (uintptr, error) {
		call.n++
		call.target = target
		call.show = showCmd
		return call.code, call.err
	}
	t.Cleanup(func() { shellOpenFn = prev })
	return call
}

func TestOpenAppOrPathNormalizesDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "my dir")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	raw := " \t" + filepath.Join(base, "my dir", "inner", "..") + " \t"
	call := trapShell(t)
	if err := OpenAppOrPath(raw); err != nil {
		t.Fatal(err)
	}
	if call.n != 1 {
		t.Fatalf("shell calls = %d", call.n)
	}
	if call.target != dir {
		t.Fatalf("target %q, want %q", call.target, dir)
	}
	if call.show != 1 {
		t.Fatalf("show flag %d, want SW_SHOWNORMAL 1", call.show)
	}
}

func TestShellExecuteOpenCleansPathAndKeepsURL(t *testing.T) {
	call := trapShell(t)
	if err := shellExecuteOpen(`C:\foo\bar\..\baz`); err != nil {
		t.Fatal(err)
	}
	if call.target != `C:\foo\baz` || call.show != 1 {
		t.Fatalf("path target %q show %d", call.target, call.show)
	}
	const raw = "https://example.com/a/b"
	if err := shellExecuteOpen("  " + raw + "  "); err != nil {
		t.Fatal(err)
	}
	if call.target != raw {
		t.Fatalf("url target %q", call.target)
	}
	if strings.Contains(call.target, `\`) {
		t.Fatalf("url was rewritten: %q", call.target)
	}
}

func TestOpenAppOrPathRejectsEmptyAndMissing(t *testing.T) {
	call := trapShell(t)
	for _, raw := range []string{"", "   ", "\t"} {
		err := OpenAppOrPath(raw)
		if err == nil || !strings.Contains(err.Error(), "路径为空") {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	missing := filepath.Join(t.TempDir(), "missing-dir")
	err := OpenAppOrPath(missing)
	if err == nil || !strings.Contains(err.Error(), "路径不存在") {
		t.Fatalf("missing: %v", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing path not in error: %v", err)
	}
	if call.n != 0 {
		t.Fatalf("shell calls = %d", call.n)
	}
}

func TestOpenAppOrPathFileDoesNotUseShellOrHide(t *testing.T) {
	file := filepath.Join(t.TempDir(), "readme.pbdata")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	call := trapShell(t)
	err := OpenAppOrPath("  " + file + "  ")
	if call.n != 0 {
		t.Fatal("file path called shell")
	}
	if err == nil {
		t.Fatal("non-exe file start returned nil")
	}
	attr := fileProcAttr()
	if attr == nil || attr.HideWindow || attr.CreationFlags&createNoWindow != 0 {
		t.Fatalf("file start hides the window: %+v", attr)
	}
}

func TestOpenURLValidation(t *testing.T) {
	call := trapShell(t)
	for _, raw := range []string{"", "ftp://example.com", "file:///C:/Windows", "HTTPS://example.com", "javascript:alert(1)"} {
		err := OpenURL(raw)
		if err == nil || !strings.Contains(err.Error(), "仅支持打开 http/https 链接") {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	if call.n != 0 {
		t.Fatalf("rejected urls called shell %d times", call.n)
	}
	if err := OpenURL("  http://example.com/a  "); err != nil {
		t.Fatal(err)
	}
	if call.target != "http://example.com/a" || call.show != 1 {
		t.Fatalf("http target %q show %d", call.target, call.show)
	}
	if err := OpenURL("https://example.com/a/b"); err != nil {
		t.Fatal(err)
	}
	if call.target != "https://example.com/a/b" || call.show != 1 {
		t.Fatalf("https target %q show %d", call.target, call.show)
	}
}

func TestShellFailureCodesPropagate(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		code uintptr
		text string
	}{
		{0, "无法打开"},
		{2, "找不到文件"},
		{3, "找不到路径"},
		{5, "拒绝访问"},
		{31, "没有关联的程序"},
		{32, "无法打开"},
	}
	for _, tc := range cases {
		t.Run(strconv.FormatUint(uint64(tc.code), 10), func(t *testing.T) {
			call := trapShell(t)
			call.code = tc.code
			err := OpenAppOrPath(dir)
			if err == nil {
				t.Fatal("expected error")
			}
			msg := err.Error()
			wantCode := "错误码 " + strconv.FormatUint(uint64(tc.code), 10)
			if !strings.Contains(msg, wantCode) || !strings.Contains(msg, tc.text) {
				t.Fatalf("error %q", msg)
			}
			if call.show != 1 {
				t.Fatalf("show flag %d", call.show)
			}
			urlErr := OpenURL("https://example.com")
			if urlErr == nil || !strings.Contains(urlErr.Error(), wantCode) {
				t.Fatalf("url error %v", urlErr)
			}
		})
	}

	call := trapShell(t)
	call.code = 33
	if err := OpenAppOrPath(dir); err != nil {
		t.Fatal(err)
	}
	if call.show != 1 {
		t.Fatalf("show flag %d", call.show)
	}
}
