Unicode true

####
## SpyDB's Windows installer (SpyDB-windows-setup.exe), built by the release
## workflow (.github/workflows/release.yml) from bin\SpyDB.exe:
##   wails3 generate webview2bootstrapper -dir build/windows/nsis
##   makensis -DARG_WAILS_AMD64_BINARY=..\..\..\bin\SpyDB.exe -DINFO_PRODUCTVERSION=0.1.9 build/windows/nsis/project.nsi
##
## It installs for the current user only, in %LOCALAPPDATA%\Programs\SpyDB: no
## administrator needed, so SpyDB can update itself (updater/install_windows.go
## runs it silently into its folder: /S /D=<folder>). Saved connections and
## queries (%AppData%\SpyDB) stay, on update and on uninstall.
####
!define INFO_PROJECTNAME "SpyDB"
!define INFO_COMPANYNAME "Anuj Kaushik"
!define INFO_PRODUCTNAME "SpyDB"
!ifndef INFO_PRODUCTVERSION
    !define INFO_PRODUCTVERSION "0.0.0" # the release's version, from the tag
!endif
!define INFO_COPYRIGHT "© 2026, Anuj Kaushik"
!define UNINST_KEY_NAME "SpyDB" # HKCU\…\Uninstall\SpyDB: updater/install_windows.go reads it
!define WAILS_INSTALL_SCOPE "user"

!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

# "SpyDB Installer" and the version are how the updater knows the download
# is this release's installer (updater/winsetup.go).
VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "Open SpyDB"

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\SpyDB-windows-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation" # an update goes where SpyDB is
ShowInstDetails show # This will always show the installation details.

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    # Windows locks a running program's file: an update may start just
    # before the old SpyDB has quit, so wait up to 10 seconds for it.
    StrCpy $R1 0
    waitForSpyDB:
        ClearErrors
        Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
        IfErrors 0 spydbClosed
        IntOp $R1 $R1 + 1
        IntCmp $R1 20 spydbStillOpen
        Sleep 500
        Goto waitForSpyDB
    spydbStillOpen:
        MessageBox MB_OK|MB_ICONEXCLAMATION "SpyDB is still open. Close it, then run this again." /SD IDOK
        SetErrorLevel 2
        Quit
    spydbClosed:

    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    # On the desktop only when installed by hand: an update (silent) doesn't
    # bring back a shortcut you deleted.
    IfSilent +2
        CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
    WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    # SpyDB's own files only, and then the folder if that leaves it empty:
    # it may have been chosen with other files in it.
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
    RMDir $INSTDIR
SectionEnd
