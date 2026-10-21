; Inno Setup definition for errkb (EKB-051).
;
; The installer manages a PostgreSQL cluster for the installed product.
; PostgreSQL stays an explicit dependency (plan §2) and the installer refuses to
; start without it. The heavy lifting lives in postgres.ps1 (packaging) and
; internal/service/pg_manage.go (decisions, credentials, upgrade rollback); this
; file only drives the wizard, asks the retention question on uninstall, and
; detects an existing instance or a busy port before choosing a cluster.
;
; Compile with Inno Setup 6:
;   iscc packaging\windows\installer.iss
;
; The wizard offers three conflict choices (plan §11 EKB-051, acceptance 5):
;   1. Create a dedicated cluster on the default port (5433, never 5432).
;   2. Reuse an existing PostgreSQL instance (no cluster is created).
;   3. Create a dedicated cluster on a different port.
#define AppName "errkb"
#define AppVersion "0.1.0"
#define AppPublisher "jingchangzhao-gif"
#define AppURL "https://github.com/jingchangzhao-gif/error-knowledge-base"
#define DefaultPgPort "5433"
#define ClusterChoiceNew "Create a dedicated cluster (recommended)"
#define ClusterChoiceExisting "Reuse an existing PostgreSQL instance"
#define ClusterChoiceOtherPort "Create a dedicated cluster on a different port"

[Setup]
AppId={{9F4A6D3E-5C1B-4E2A-9B7D-2E6F1A8C0D54}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
DefaultDirName={localappdata}\errkb
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
DisableWelcomePage=no
PrivilegesRequired=lowest
OutputDir=..\..\dist
OutputBaseFilename=errkb-{#AppVersion}-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesInstallIn64BitMode=x64compatible

[Files]
Source: "..\..\dist\errkb.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "postgres.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "autostart.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "start-errkb.cmd"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\db\migrations\*.sql"; DestDir: "{app}\migrations"; Flags: ignoreversion
Source: "..\..\README.md"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist

[Dirs]
Name: "{app}\pg"; Permissions: users-full

[Icons]
; Open the toolbox without a terminal: a hidden PowerShell hosts errkb ui --web,
; and the launcher stays available for manual use.
Name: "{group}\Start Error Knowledge Base"; Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -Command ""& '{app}\errkb.exe' ui --web"""; WorkingDir: "{app}"; IconFilename: "{app}\errkb.exe"
Name: "{group}\Uninstall Error Knowledge Base"; Filename: "{uninstallexe}"
[Run]
; Choice 1 or 3: create and start the dedicated cluster.
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\postgres.ps1"" install -DataDir ""{app}\pg\data"" -Port {code:GetPgPort}"; StatusMsg: "Creating the local PostgreSQL cluster (this takes a few seconds)..."; Flags: runhidden waituntilterminated; Check: CreatesCluster
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\postgres.ps1"" start -DataDir ""{app}\pg\data"" -Port {code:GetPgPort}"; StatusMsg: "Starting the local PostgreSQL cluster..."; Flags: runhidden waituntilterminated; Check: CreatesCluster
; EKB-056: a fresh install creates the schema with the shipped CLI (there is no
; Go toolchain on the target machine). The daemon refuses to start until the
; ledger matches, so this step is what makes the first run work.
Filename: "{app}\errkb.exe"; Parameters: "db migrate --credentials ""{app}\pg\credentials.json"" --migrations ""{app}\migrations"""; StatusMsg: "Creating the errkb database schema..."; Flags: runhidden waituntilterminated; Check: CreatesCluster
; EKB-056: one logon ScheduledTask starts the cluster and daemon again after a
; reboot; it also starts both now, so the product works right after setup.
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\autostart.ps1"" -Action install -AppDir ""{app}"" -Port {code:GetPgPort}"; StatusMsg: "Configuring start at logon..."; Flags: runhidden waituntilterminated; Check: CreatesCluster
; Do not launch a console program from a silent install. On Windows 11 the
; default terminal host can outlive the command, which leaves automation
; waiting indefinitely for the post-install process to exit.
Filename: "{app}\errkb.exe"; Parameters: "version"; Description: "Show the errkb version"; Flags: postinstall shellexec runasoriginaluser skipifsilent

[UninstallRun]
; Stop the daemon and drop the logon Run entry before the files disappear (a
; running daemon would keep errkb.exe locked and the Run entry would point at
; nothing).
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\autostart.ps1"" -Action uninstall -AppDir ""{app}"""; Flags: runhidden waituntilterminated; RunOnceId: "ErrkbAutostart"
; The uninstaller keeps the local database data by default. A silent uninstall
; cannot answer a dialog, so the choice is a command-line switch:
;   unins000.exe /VERYSILENT /DELETE_DATA=1   -> remove the cluster data
;   unins000.exe /VERYSILENT                  -> keep the data
; The switch is expanded by the uninstaller's own command line, which means it
; also works in the second phase that actually runs [UninstallRun].
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\postgres.ps1"" uninstall -DataDir ""{app}\pg\data"" -Port {code:GetPgPort} -Choice {param:DELETE_DATA|keep}"; Flags: runhidden waituntilterminated; RunOnceId: "ErrkbPg"

[Code]
var
  ClusterPage: TInputOptionWizardPage;
  PortPage: TInputQueryWizardPage;
  PortChosen: Integer;

function PostgresBinPresent(): Boolean;
var
  Candidate: String;
begin
  Result := False;
  Candidate := ExpandConstant('{pf}\PostgreSQL\16\bin\pg_ctl.exe');
  if FileExists(Candidate) then
    Result := True;
  if not Result then
  begin
    Candidate := ExpandConstant('{pf32}\PostgreSQL\16\bin\pg_ctl.exe');
    if FileExists(Candidate) then
      Result := True;
  end;
  if not Result then
  begin
    Candidate := ExpandConstant('{env:ERRKB_PG_BIN}\pg_ctl.exe');
    if FileExists(Candidate) then
      Result := True;
  end;
end;

function PortInUse(Port: Integer): Boolean;
var
  ResultCode: Integer;
  Command: String;
begin
  Command := '-NoProfile -Command "if ((New-Object System.Net.Sockets.TcpClient).ConnectAsync(''127.0.0.1'',' + IntToStr(Port) + ').Wait(400)) { exit 3 } else { exit 0 }"';
  Exec('powershell.exe', Command, '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Result := (ResultCode = 3);
end;

function ClusterDataPresent(): Boolean;
begin
  Result := FileExists(ExpandConstant('{app}\pg\data\PG_VERSION'));
end;

procedure InitializeWizard();
begin
  ClusterPage := CreateInputOptionPage(wpSelectDir,
    'Local database', 'How should errkb use PostgreSQL?',
    'errkb needs PostgreSQL 16. It is an explicit dependency; this installer can manage a dedicated local cluster for you.',
    False, False);
  ClusterPage.Add('{#ClusterChoiceNew}');
  ClusterPage.Add('{#ClusterChoiceExisting}');
  ClusterPage.Add('{#ClusterChoiceOtherPort}');
  ClusterPage.Values[0] := True;

  PortPage := CreateInputQueryPage(ClusterPage.ID,
    'Local database port', 'Which port should the dedicated cluster listen on?',
    'The default 5433 leaves 5432 to an existing PostgreSQL instance. The installer refuses a port that is already in use.');
  PortPage.Add('Port:', False);
  PortPage.Values[0] := '{#DefaultPgPort}';
  PortChosen := StrToInt('{#DefaultPgPort}');
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := False;
  if PageID = PortPage.ID then
    Result := not (ClusterPage.Values[0] or ClusterPage.Values[2]);
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  Port: Integer;
begin
  Result := True;
  if CurPageID = PortPage.ID then
  begin
    Port := StrToIntDef(PortPage.Values[0], 0);
    if (Port < 1024) or (Port > 65535) then
    begin
      MsgBox('The port must be between 1024 and 65535.', mbError, MB_OK);
      Result := False;
      Exit;
    end;
    if PortInUse(Port) then
    begin
      MsgBox('Port ' + IntToStr(Port) + ' is already in use. Pick another port, or choose "Reuse an existing PostgreSQL instance".', mbError, MB_OK);
      Result := False;
      Exit;
    end;
    PortChosen := Port;
  end;

  if CurPageID = ClusterPage.ID then
  begin
    if (ClusterPage.Values[0] or ClusterPage.Values[2]) and ClusterDataPresent() then
    begin
      if MsgBox('A cluster already exists in ' + ExpandConstant('{app}\pg\data') + '.' + #13#10 +
        'Continue and reuse it? Choose No to pick another data directory or remove it first.', mbConfirmation, MB_YESNO) = IDNO then
      begin
        Result := False;
        Exit;
      end;
    end;
  end;
end;

function CreatesCluster(): Boolean;
begin
  Result := (ClusterPage.Values[0] or ClusterPage.Values[2]) and PostgresBinPresent();
end;

function GetPgPort(Param: String): String;
begin
  Result := IntToStr(PortChosen);
end;

function UninstallParam(Name: String): Boolean;
var
  I: Integer;
  Param: String;
  LowerName: String;
begin
  Result := False;
  LowerName := Lowercase(Name);
  for I := 1 to ParamCount do
  begin
    Param := Lowercase(ParamStr(I));
    if Param = LowerName then
    begin
      Result := True;
      Exit;
    end;
    if Copy(Param, 1, Length(LowerName) + 1) = LowerName + '=' then
    begin
      Result := Param <> LowerName + '=0';
      Exit;
    end;
  end;
end;

procedure SetDeleteDataMarker(Remove: Boolean);
var
  Marker: String;
begin
  Marker := ExpandConstant('{app}\.errkb-delete-data');
  if Remove then
  begin
    if FileExists(Marker) then DeleteFile(Marker);
  end
  else
    SaveStringToFile(Marker, '1', False);
end;

function InitializeUninstall(): Boolean;
var
  Choice: Integer;
begin
  Result := True;
  if ClusterDataPresent() then
  begin
    { A dialog answer cannot travel to the second uninstaller phase that runs
      the uninstall commands, so the choice is written as a marker file next
      to the database and postgres.ps1 reads it. }
    if UninstallParam('/delete_data') or UninstallParam('/delete_data=1') then
    begin
      SetDeleteDataMarker(False);
      Exit;
    end;
    if UninstallParam('/keep_data') or UninstallParam('/keep_data=1') then
    begin
      SetDeleteDataMarker(True);
      Exit;
    end;
    Choice := MsgBox('Keep the local database data?', mbConfirmation, MB_YESNOCANCEL);
    if Choice = IDCANCEL then
    begin
      Result := False;
      Exit;
    end;
    SetDeleteDataMarker(Choice = IDYES);
  end;
end;

function InitializeSetup(): Boolean;
begin
  Result := True;
  if not PostgresBinPresent() then
  begin
    MsgBox('PostgreSQL 16 was not found.' + #13#10 +
      'errkb needs PostgreSQL 16 (an explicit dependency of the release). Install it first, ' +
      'or set ERRKB_PG_BIN to its bin directory, then run this installer again.', mbCriticalError, MB_OK);
    Result := False;
  end;
end;
