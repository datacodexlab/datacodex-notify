; Inno Setup script for DataCodex Notify (per-user install, no administrator rights).
; Build: installer\build.ps1 (it passes AppVersion and, for tests, AppGuid and OutputName).

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef AppGuid
  #define AppGuid "{{6D1A4C52-3B7E-4F0A-9C58-0D2E7A91B4F3}"
#endif
#ifndef OutputName
  #define OutputName "DatacodexNotify-Setup-" + AppVersion
#endif

#define AppExe "datacodex-notify.exe"
#define CliExe "datacodex-notify-cli.exe"
; The Run value the app itself uses for "Start with Windows" (internal/autostart).
#define RunValue "DatacodexNotify"

[Setup]
AppId={#AppGuid}
AppName=DataCodex Notify
AppVersion={#AppVersion}
AppVerName=DataCodex Notify {#AppVersion}
AppPublisher=DataCodex
DefaultDirName={localappdata}\Programs\DatacodexNotify
DefaultGroupName=DataCodex Notify
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=..\dist
OutputBaseFilename={#OutputName}
SetupIconFile=..\assets\icon-green.ico
UninstallDisplayIcon={app}\{#AppExe}
UninstallDisplayName=DataCodex Notify
LicenseFile=..\LICENSE
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=no
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "startup"; Description: "Start DataCodex Notify when I sign in to Windows"; Flags: unchecked
Name: "skill"; Description: "Install the agent skill for Claude Code and Codex"; Flags: unchecked

[Files]
Source: "..\dist\{#AppExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\{#CliExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\DataCodex Notify"; Filename: "{app}\{#AppExe}"
Name: "{group}\Uninstall DataCodex Notify"; Filename: "{uninstallexe}"

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "{#RunValue}"; ValueData: """{app}\{#AppExe}"""; Tasks: startup

[Run]
Filename: "{app}\{#CliExe}"; Parameters: "skill install"; Tasks: skill; Flags: runhidden; StatusMsg: "Installing the agent skill..."
Filename: "{app}\{#AppExe}"; Description: "Start DataCodex Notify"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{app}\{#CliExe}"; Parameters: "skill remove --install-dir ""{app}"""; Flags: runhidden; RunOnceId: "RemoveSkill"

[Code]
const
  RunKey = 'Software\Microsoft\Windows\CurrentVersion\Run';

{ Stops only the copy of the app that lives in the install folder, never another one. }
procedure StopInstalledApp;
var
  ResultCode: Integer;
  Command: String;
begin
  Command := '-NoProfile -ExecutionPolicy Bypass -Command "Get-Process datacodex-notify -ErrorAction SilentlyContinue | ' +
    'Where-Object { $_.Path -eq ''' + ExpandConstant('{app}\{#AppExe}') + ''' } | Stop-Process -Force"';
  Exec('powershell.exe', Command, '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Sleep(1200);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  { An upgrade replaces the running program, so stop it first. Data is not touched. }
  StopInstalledApp;
  Result := '';
end;

function InitializeUninstall: Boolean;
begin
  StopInstalledApp;
  Result := True;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  RunValue, DataDir: String;
begin
  if CurUninstallStep = usUninstall then
  begin
    { Remove the Start-with-Windows entry, but only when it points at this install. }
    if RegQueryStringValue(HKCU, RunKey, '{#RunValue}', RunValue) then
    begin
      if (Pos(Lowercase(ExpandConstant('{app}')), Lowercase(RunValue)) > 0) or
         (Pos(Lowercase(GetShortName(ExpandConstant('{app}'))), Lowercase(RunValue)) > 0) then
        RegDeleteValue(HKCU, RunKey, '{#RunValue}');
    end;
  end;

  if CurUninstallStep = usPostUninstall then
  begin
    { Ask before deleting the user's data. A silent uninstall always keeps it. }
    DataDir := ExpandConstant('{localappdata}\DatacodexNotify');
    if DirExists(DataDir) and (not UninstallSilent) then
      if MsgBox('Also delete your DataCodex Notify data?' + #13#10 + #13#10 +
        DataDir + #13#10 + #13#10 +
        'This removes the WhatsApp link, the message log, and your settings. ' +
        'You will have to link WhatsApp again if you install the app later.' + #13#10 + #13#10 +
        'Choose No to keep it.', mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
        DelTree(DataDir, True, True, True);
  end;
end;
