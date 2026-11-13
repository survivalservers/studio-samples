package sdk

// Every language's sample runs under the command-line tool's dev command (oss/localapi/dev) and must make the same calls
// with the same results. A language whose toolchain is not installed is skipped (failed, with
// GS_SDK_TOOLCHAINS=required, as on our check runner); the Windows builds are compiled with MinGW and run on the
// Windows test machine.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// devagent is the command-line tool, run as "survivalservers dev".
var devagent string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gssdk-test-")
	if err != nil {
		panic(err)
	}
	devagent = filepath.Join(dir, "survivalservers")
	if out, err := exec.Command("go", "build", "-C", "../cli", "-o", devagent, "./cmd/survivalservers").CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// need skips a test whose toolchain is not installed. On our check runner (GS_SDK_TOOLCHAINS=required) a missing
// Linux toolchain fails the test instead, so a layer never passes there by not running.
func need(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			missing(t, tool+" is not installed")
		}
	}
}

func missing(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("GS_SDK_TOOLCHAINS") == "required" {
		t.Fatal(why + " (GS_SDK_TOOLCHAINS=required)")
	}
	t.Skip(why)
}

func build(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// underAgent runs a sample as a ready server (pre-started) that gets match m42 with MAP=dunes, and checks every call.
// It runs twice: with today's names, and with the older ones (GS_KIND=world, and the local API's address in
// GS_AGENT_URL instead of GS_LOCAL_API_URL). The layers read the address under either name and never GET /v0/self,
// so a build must make the same calls with the same results.
func underAgent(t *testing.T, env []string, program ...string) {
	t.Helper()
	for _, host := range []struct {
		name  string
		flags []string
		env   []string
		shows string
	}{
		{"current names", nil, nil, "GS_KIND=match"},
		{"older names", []string{"-kind", "world"}, []string{"older"}, "GS_KIND=world"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		args := append([]string{"dev", "-port", "0", "-warm", "-allocate-after", "300ms", "-env", "MAP=dunes", "-external-id", "m42",
			"-shutdown-time", "5s", "-stop", "local_api", "-dir", t.TempDir()}, host.flags...)
		run := program
		if len(host.env) > 0 {
			// The local API's address under its older name only, as an older machine gave it.
			run = append([]string{"sh", "-c", `GS_AGENT_URL=$GS_LOCAL_API_URL; export GS_AGENT_URL; unset GS_LOCAL_API_URL; exec "$@"`, "sh"}, program...)
		}
		cmd := exec.CommandContext(ctx, devagent, append(append(args, "--"), run...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		cancel()
		got := string(out)
		if err != nil {
			t.Fatalf("%s: survivalservers dev: %v\n%s", host.name, err, got)
		}
		for _, want := range []string{host.shows, "match m42 map=dunes", "players: 1 [p1]", "players: 2 [p1 p2]",
			"refused a player ID with a control character", "labels: mode=ffa", "health", "tick: p99 6.25 ms p50 2.50 ms at 64 Hz",
			`the server asked to stop: "match_over"`, "shutdown reason=self deadline=set", "exited after the stop request with code 0"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: missing %q in:\n%s", host.name, want, got)
			}
		}
		if i, j := strings.LastIndex(got, "players: 2 [p1 p2]"), strings.LastIndex(got, "players: 1 [p1]"); j < i {
			t.Fatalf("%s: the player who left is still counted:\n%s", host.name, got)
		}
	}
}

func TestC(t *testing.T) {
	need(t, "cc")
	bin := filepath.Join(t.TempDir(), "sample")
	build(t, ".", "cc", "-std=c99", "-Wall", "-Wextra", "-pedantic", "-Werror", "-D_POSIX_C_SOURCE=200809L", "-Ic/include",
		"c/sample/sample.c", "c/src/gs_sdk.c", "-lpthread", "-o", bin)
	underAgent(t, nil, bin)
}

func TestCPlusPlus(t *testing.T) {
	need(t, "cc", "c++")
	dir := t.TempDir()
	build(t, ".", "cc", "-std=c99", "-Wall", "-Werror", "-D_POSIX_C_SOURCE=200809L", "-Ic/include", "-c", "c/src/gs_sdk.c", "-o", dir+"/gs_sdk.o")
	build(t, ".", "c++", "-std=c++17", "-Wall", "-Wextra", "-Werror", "-Ic/include", "-Icpp", "cpp/sample.cpp", dir+"/gs_sdk.o", "-lpthread", "-o", dir+"/sample")
	underAgent(t, nil, dir+"/sample")
}

func TestGo(t *testing.T) {
	need(t, "cc")
	bin := filepath.Join(t.TempDir(), "sample")
	build(t, ".", "go", "build", "-o", bin, "./go/sample")
	underAgent(t, nil, bin)
}

func TestCSharp(t *testing.T) {
	need(t, "cc", "dotnet")
	dir := t.TempDir()
	build(t, ".", "cc", "-std=c99", "-O2", "-shared", "-fPIC", "-fvisibility=hidden", "-DGS_SDK_SHARED", "-D_POSIX_C_SOURCE=200809L",
		"-Ic/include", "c/src/gs_sdk.c", "-lpthread", "-o", dir+"/libgs_sdk.so")
	// Build from a copy so obj/ and bin/ stay out of the tree, with no compiler or MSBuild server left running.
	if err := copyTree("csharp", dir+"/csharp"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("dotnet", "build", dir+"/csharp/Sample", "-c", "Release", "-o", dir+"/out", "--nologo", "-v", "q",
		"--disable-build-servers", "-p:UseSharedCompilation=false", "-nodeReuse:false")
	cmd.Env = append(os.Environ(), "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1", "MSBUILDDISABLENODEREUSE=1",
		"DOTNET_CLI_USE_MSBUILD_SERVER=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dotnet build: %v\n%s", err, out)
	}
	underAgent(t, []string{"LD_LIBRARY_PATH=" + dir, "DOTNET_CLI_TELEMETRY_OPTOUT=1"}, dir+"/out/Sample")
}

// copyTree copies the files under src to dst, leaving out build output.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			if d.Name() == "obj" || d.Name() == "bin" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

// findCargo returns cargo from PATH or ~/.cargo/bin, or "" when Rust is not installed.
func findCargo() string {
	if cargo, err := exec.LookPath("cargo"); err == nil {
		return cargo
	}
	if home, _ := os.UserHomeDir(); home != "" {
		if _, err := os.Stat(home + "/.cargo/bin/cargo"); err == nil {
			return home + "/.cargo/bin/cargo"
		}
	}
	return ""
}

func TestRust(t *testing.T) {
	cargo := findCargo()
	if cargo == "" {
		missing(t, "cargo is not installed")
	}
	target := t.TempDir()
	build(t, "rust", cargo, "test", "--offline", "--locked", "--quiet", "--target-dir", target)
	build(t, "rust", cargo, "build", "--offline", "--locked", "--quiet", "--example", "sample", "--target-dir", target)
	underAgent(t, nil, filepath.Join(target, "debug", "examples", "sample"))
}

// TestWindowsBuilds compiles the library (static and as gs_sdk.dll), the C and C++ samples, the Go layer and (when
// the Rust windows-gnu target is installed) the Rust sample for Windows with MinGW. They run on the Windows test
// machine.
func TestWindowsBuilds(t *testing.T) {
	for _, tool := range []string{"x86_64-w64-mingw32-gcc", "x86_64-w64-mingw32-g++"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool) // optional everywhere: the Windows builds are checked on the Windows test machine
		}
	}
	dir := t.TempDir()
	cc, cxx := "x86_64-w64-mingw32-gcc", "x86_64-w64-mingw32-g++"
	build(t, ".", cc, "-std=c99", "-Wall", "-Wextra", "-Werror", "-O2", "-Ic/include", "-c", "c/src/gs_sdk.c", "-o", dir+"/gs_sdk.o")
	build(t, ".", cc, "-std=c99", "-O2", "-shared", "-DGS_SDK_SHARED", "-Ic/include", "c/src/gs_sdk.c", "-lws2_32", "-o", dir+"/gs_sdk.dll",
		"-Wl,--out-implib,"+dir+"/libgs_sdk.dll.a")
	build(t, ".", cc, "-std=c99", "-Ic/include", "c/sample/sample.c", dir+"/gs_sdk.o", "-lws2_32", "-o", dir+"/sample.exe")
	build(t, ".", cxx, "-std=c++17", "-Wall", "-Werror", "-Ic/include", "-Icpp", "cpp/sample.cpp", dir+"/gs_sdk.o", "-lws2_32",
		"-static", "-o", dir+"/sample_cpp.exe")
	cmd := exec.Command("go", "build", "-o", dir+"/sample_go.exe", "./go/sample")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=1", "CC="+cc)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go for windows: %v\n%s", err, out)
	}
	files := []string{"gs_sdk.dll", "sample.exe", "sample_cpp.exe", "sample_go.exe"}
	if cargo := findCargo(); cargo != "" {
		if out, err := exec.Command(filepath.Dir(cargo)+"/rustup", "target", "list", "--installed").Output(); err == nil &&
			strings.Contains(string(out), "x86_64-pc-windows-gnu") {
			build(t, "rust", cargo, "build", "--offline", "--locked", "--quiet", "--example", "sample", "--target", "x86_64-pc-windows-gnu",
				"--target-dir", dir+"/rust")
			files = append(files, "rust/x86_64-pc-windows-gnu/debug/examples/sample.exe")
		}
	}
	for _, f := range files {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.Size() == 0 {
			t.Fatalf("%s not built", f)
		}
	}
}
