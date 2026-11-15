package updater

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

// versionResource is the part of a version resource checkSetup reads: each
// key, NUL padding, its value, as Windows lays them out.
func versionResource(pairs ...string) []byte {
	b := []byte("MZ\x90\x00 some program bytes ")
	for i := 0; i+1 < len(pairs); i += 2 {
		b = append(b, 0, 0, 0, 0) // wLength, wValueLength…
		b = append(b, utf16Bytes(pairs[i]+"\x00")...)
		b = append(b, 0, 0) // padding to 32 bits
		b = append(b, utf16Bytes(pairs[i+1]+"\x00")...)
	}
	return append(b, " more bytes"...)
}

func TestCheckSetup(t *testing.T) {
	setup := versionResource("CompanyName", "Anuj Kaushik", "FileDescription", "SpyDB Installer", "ProductVersion", "0.1.9", "ProductName", "SpyDB")
	if err := checkSetup(setup, "0.1.9"); err != nil {
		t.Errorf("the 0.1.9 installer: %v", err)
	}
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"another version", setup, "not the SpyDB 0.1.8 installer"},
		{"SpyDB.exe, not its installer", versionResource("FileDescription", "SpyDB", "ProductVersion", "0.1.8"), "not the SpyDB 0.1.8 installer"},
		{"another program", versionResource("FileDescription", "Other Installer", "ProductVersion", "0.1.8"), "not the SpyDB 0.1.8 installer"},
		{"no version resource", []byte("MZ just a program"), "not the SpyDB 0.1.8 installer"},
		{"not a program", []byte("<html>404</html>"), "isn't a Windows program"},
		{"empty", nil, "isn't a Windows program"},
	} {
		if err := checkSetup(c.data, "0.1.8"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want it to mention %q", c.name, err, c.want)
		}
	}
	// A value that never ends (a damaged file) reads as none.
	if v := versionString(append(utf16Bytes("ProductVersion\x00"), utf16Bytes(strings.Repeat("9", 500))...), "ProductVersion"); v != "" {
		t.Errorf("unended value read as %q", v)
	}
}

// A real NSIS installer, when the test is given one (SPYDB_SETUP=path and
// SPYDB_SETUP_VERSION=0.1.9): what makensis writes is what checkSetup reads.
func TestCheckSetupReal(t *testing.T) {
	path, version := os.Getenv("SPYDB_SETUP"), os.Getenv("SPYDB_SETUP_VERSION")
	if path == "" {
		t.Skip("set SPYDB_SETUP to an installer built by makensis")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSetup(data, version); err != nil {
		t.Error(err)
	}
	if err := checkSetup(data, version+".1"); err == nil {
		t.Error("another version passed")
	}
}

func TestPSQuote(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\anuj\AppData\Local\Programs\SpyDB`: `'C:\Users\anuj\AppData\Local\Programs\SpyDB'`,
		`C:\Users\O'Brien\SpyDB`:                     `'C:\Users\O''Brien\SpyDB'`,
		`C:\Users\Anuj’s PC\SpyDB`:                   `'C:\Users\Anuj’’s PC\SpyDB'`,  // PowerShell takes ’ for ' too
		`C:\$env:x\"quoted"\$(calc)`:                 `'C:\$env:x\"quoted"\$(calc)'`, // nothing runs inside '…'
	} {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestHelperScript(t *testing.T) {
	got := helperScript(4242, `C:\Temp\spydb-update-1\SpyDB-windows-setup.exe`, `C:\Users\O'Brien\AppData\Local\Programs\SpyDB`,
		`C:\Users\O'Brien\AppData\Local\Programs\SpyDB\SpyDB.exe`, `C:\Temp\spydb-update-1`)
	want := `$ErrorActionPreference = 'SilentlyContinue'
Wait-Process -Id 4242 -Timeout 60
Start-Process -FilePath 'C:\Temp\spydb-update-1\SpyDB-windows-setup.exe' -ArgumentList '/S /D=C:\Users\O''Brien\AppData\Local\Programs\SpyDB' -Wait
Start-Process -FilePath 'C:\Users\O''Brien\AppData\Local\Programs\SpyDB\SpyDB.exe'
Remove-Item -LiteralPath 'C:\Temp\spydb-update-1' -Recurse -Force`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	// -EncodedCommand: Base64 of the script as UTF-16, little end first.
	raw, err := base64.StdEncoding.DecodeString(encodedCommand("Wait-Process ’é"))
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("%v, %d bytes", err, len(raw))
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if back := string(utf16.Decode(units)); back != "Wait-Process ’é" {
		t.Errorf("decoded %q", back)
	}
}
