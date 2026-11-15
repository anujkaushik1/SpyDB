package updater

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// The parts of the Windows update (install_windows.go) that don't need
// Windows, here so every platform's tests check them.

// setupName is the release's Windows installer.
const setupName = "SpyDB-windows-setup.exe"

// checkSetup says whether data is release version's installer, as its
// version resource (what File Explorer's Properties → Details shows) says:
// "SpyDB Installer", version. A program that isn't one, another version,
// or SpyDB.exe itself fails.
func checkSetup(data []byte, version string) error {
	if len(data) < 2 || string(data[:2]) != "MZ" {
		return errors.New("The download isn't a Windows program. Nothing was changed.")
	}
	description, got := versionString(data, "FileDescription"), versionString(data, "ProductVersion")
	if description != "SpyDB Installer" || got != version {
		return fmt.Errorf("The download is %q %s, not the SpyDB %s installer. Nothing was changed.", description, got, version)
	}
	return nil
}

// versionString finds key's value in a Windows version resource: the key,
// then the value, both UTF-16 text ending in a NUL, with NUL padding
// between them. "" if there's no such key.
func versionString(data []byte, key string) string {
	k := utf16Bytes(key + "\x00")
	i := bytes.Index(data, k)
	if i < 0 {
		return ""
	}
	j := i + len(k)
	for j+1 < len(data) && data[j] == 0 && data[j+1] == 0 {
		j += 2
	}
	var value []uint16
	for ; j+1 < len(data); j += 2 {
		c := uint16(data[j]) | uint16(data[j+1])<<8
		if c == 0 {
			return string(utf16.Decode(value))
		}
		if value = append(value, c); len(value) > 128 {
			break
		}
	}
	return ""
}

func utf16Bytes(s string) []byte {
	var b []byte
	for _, c := range utf16.Encode([]rune(s)) {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// helperScript is the PowerShell that finishes an update once SpyDB has
// quit (Windows locks a running program's file): wait for process pid (up
// to a minute), run setup silently into dir (/D= must come last, unquoted),
// start exe again and remove leftover.
func helperScript(pid int, setup, dir, exe, leftover string) string {
	return strings.Join([]string{
		"$ErrorActionPreference = 'SilentlyContinue'",
		fmt.Sprintf("Wait-Process -Id %d -Timeout 60", pid),
		fmt.Sprintf("Start-Process -FilePath %s -ArgumentList %s -Wait", psQuote(setup), psQuote("/S /D="+dir)),
		fmt.Sprintf("Start-Process -FilePath %s", psQuote(exe)),
		fmt.Sprintf("Remove-Item -LiteralPath %s -Recurse -Force", psQuote(leftover)),
	}, "\n")
}

// psQuote is s as a PowerShell string that nothing in it can end early:
// single quotes, where only a quote is special, written twice. PowerShell
// takes the curly ones (’ in "Anuj’s PC") for ' too.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		if strings.ContainsRune("'‘’‚‛", r) {
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// encodedCommand is script as powershell -EncodedCommand takes it (Base64
// of UTF-16), so no path in it needs quoting on the command line.
func encodedCommand(script string) string {
	return base64.StdEncoding.EncodeToString(utf16Bytes(script))
}
