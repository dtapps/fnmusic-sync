//go:build fpk

package webui

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildFakeFpk 构造一个与真实 fpk 结构一致的假包：
// 外层 tar.gz 含 app.tgz（内部再打包 fnmusic-sync/ui/config）、cmd/、manifest、ICON.PNG。
func buildFakeFpk(t *testing.T) string {
	t.Helper()

	// 内层 app.tgz
	var inner bytes.Buffer
	iz := gzip.NewWriter(&inner)
	iw := tar.NewWriter(iz)
	innerFiles := []struct {
		name string
		body string
		mode int64
	}{
		{"fnmusic-sync", "#!/bin/sh\necho hi\n", 0o755},
		{"ui/config/x", "{}", 0o644},
		{"ui/images/icon_64.png", "PNG", 0o644},
		{"config/privilege", "x", 0o644},
	}
	for _, f := range innerFiles {
		hdr := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}
		if err := iw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := iw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	iw.Close()
	iz.Close()

	// 外层 tar.gz
	fpk := filepath.Join(t.TempDir(), "fake.fpk")
	f, err := os.Create(fpk)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// app.tgz 作为普通文件
	appTgzBytes := inner.Bytes()
	hdr := &tar.Header{Name: "app.tgz", Mode: 0o644, Size: int64(len(appTgzBytes)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(appTgzBytes); err != nil {
		t.Fatal(err)
	}
	// 顶层目录与文件
	top := []struct {
		name string
		dir  bool
		body string
	}{
		{"cmd/main", false, "x"},
		{"manifest", false, "version=1"},
		{"ICON.PNG", false, "P"},
		{"wizard", true, ""},
	}
	for _, x := range top {
		if x.dir {
			hdr := &tar.Header{Name: x.name, Mode: 0o755, Typeflag: tar.TypeDir}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatal(err)
			}
		} else {
			hdr := &tar.Header{Name: x.name, Mode: 0o644, Size: int64(len(x.body)), Typeflag: tar.TypeReg}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(x.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return fpk
}

func TestExtractTarGz_FakeFpk(t *testing.T) {
	fpk := buildFakeFpk(t)
	dest := t.TempDir()
	if err := extractTarGz(fpk, dest); err != nil {
		t.Fatalf("extractTarGz outer failed: %v", err)
	}
	// 外层应解出 app.tgz / cmd / manifest / ICON.PNG / wizard
	for _, p := range []string{"app.tgz", "cmd/main", "manifest", "ICON.PNG", "wizard"} {
		if _, err := os.Stat(filepath.Join(dest, p)); err != nil {
			t.Errorf("外层缺少 %s: %v", p, err)
		}
	}
	// 再解 app.tgz
	appDir := filepath.Join(dest, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(filepath.Join(dest, "app.tgz"), appDir); err != nil {
		t.Fatalf("extractTarGz inner failed: %v", err)
	}
	for _, p := range []string{"fnmusic-sync", "ui/config/x", "ui/images/icon_64.png", "config/privilege"} {
		fp := filepath.Join(appDir, p)
		fi, err := os.Stat(fp)
		if err != nil {
			t.Errorf("内层缺少 %s: %v", p, err)
			continue
		}
		if filepath.Base(p) == "fnmusic-sync" && fi.Mode().Perm() != 0o755 {
			t.Errorf("fnmusic-sync 权限应为 0755，实际 %o", fi.Mode().Perm())
		}
	}
}

func TestSafeJoin_Containment(t *testing.T) {
	base := "/tmp/staging"
	// 穿透尝试必须被限制在 base 内
	for _, name := range []string{"../../etc/passwd", "/etc/passwd", "a/../../b"} {
		got, err := safeJoin(base, name)
		if err != nil {
			t.Errorf("safeJoin(%q) 不应报错: %v", name, err)
			continue
		}
		baseClean := filepath.Clean(base)
		if got != baseClean && !strings.HasPrefix(got, baseClean+string(os.PathSeparator)) {
			t.Errorf("safeJoin(%q) 逃逸 base: %s", name, got)
		}
	}
	// 纯 ".." 必须被拒绝
	if _, err := safeJoin(base, ".."); err == nil {
		t.Error("safeJoin(\"..\") 应报错")
	}
	// 合法相对路径正常
	if _, err := safeJoin(base, "ui/config"); err != nil {
		t.Errorf("合法路径不应报错: %v", err)
	}
}
