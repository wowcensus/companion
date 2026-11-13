#define AppVersion GetEnv("VERSION")
#define SourceExe GetEnv("SOURCE_EXE")
#define OutputDir GetEnv("OUTPUT_DIR")

[Setup]
AppId=io.wowcensus.companion
AppName=WoWCensus Companion
AppVersion={#AppVersion}
AppPublisher=wowcensus.io
AppPublisherURL=https://wowcensus.io
DefaultDirName=C:\WoWCensus
DisableDirPage=yes
DefaultGroupName=WoWCensus Companion
PrivilegesRequired=lowest
CloseApplications=yes
RestartApplications=no
UninstallDisplayIcon={app}\wowcensus-companion.exe
OutputDir={#OutputDir}
OutputBaseFilename=WoWCensus-Companion-{#AppVersion}-windows-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

[Files]
Source: "{#SourceExe}"; DestDir: "{app}"; DestName: "wowcensus-companion.exe"; Flags: ignoreversion

[Icons]
Name: "{userprograms}\WoWCensus Companion"; Filename: "{app}\wowcensus-companion.exe"

[Run]
Filename: "{app}\wowcensus-companion.exe"; Description: "Open WoWCensus Companion"; Flags: nowait postinstall skipifsilent

[Code]
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    RegDeleteValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run', 'WoWCensusCompanion');
end;
