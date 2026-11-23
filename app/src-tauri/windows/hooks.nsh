; Hooks for the NSIS installer Tauri builds (bundle.windows.nsis.installerHooks).
;
; The template knows the window and nothing of the service. The service holds
; strelkad.exe and the engines open while it runs and outlives the window, so
; it is stopped before files are copied and registered after, and the
; uninstaller takes it and the guard's filters away before the files go.
;
; Exit codes are checked: a service that silently failed to install looks to
; the user like "the window cannot find the service", with nothing to go on.
; $UpdateMode, $DeleteAppDataCheckboxState are the template's own variables
; (installer.nsi of tauri-cli 2.12.5).

; nsExec reads a program's output in the ANSI code page; strelkad prints
; UTF-8 unless told otherwise, and its Russian lines came out as mojibake in
; the log. The variable is inherited by every command a hook runs.
!macro STRELKA_ANSI_OUTPUT
  System::Call 'kernel32::SetEnvironmentVariable(t "STRELKA_OUTPUT", t "ansi")'
!macroend

; Astra was called Strelka up to 0.1.x. The template derives the install
; folder, the Start menu shortcut, the Apps entry and the autostart value from
; the product name, so a Strelka install is not this product's to the template:
; without this it would stay next to the new one, and its uninstaller would take
; the service both share. The service keeps its registered name and its data in
; ProgramData\Strelka; POSTINSTALL points it at the new folder.
!define OLD_PRODUCT "Strelka"
!define OLD_UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Strelka"
!define OLD_MANUPRODUCTKEY "Software\Strelka\Strelka"
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define APPROVED_KEY "Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run"

Var FoldedStrelka
Var OldAutostart
Var OldDesktop

!macro ASTRA_FOLD_IN_STRELKA
  StrCpy $FoldedStrelka 0
  ReadRegStr $R0 HKLM "${OLD_MANUPRODUCTKEY}" ""
  ; Only a folder that is plainly the old install; anything else is left be.
  ${If} $R0 != ""
  ${AndIf} $R0 != $INSTDIR
  ${AndIf} ${FileExists} "$R0\strelkad.exe"
  ${AndIf} ${FileExists} "$R0\uninstall.exe"
    DetailPrint "Переношу Strelka в Astra: $R0"
    ; Its window, if open: the template only looks for the new exe name.
    ; Matched by full path, so no other program called strelka is touched.
    nsExec::ExecToLog `powershell.exe -NoProfile -NonInteractive -Command "Get-Process -Name strelka -ErrorAction SilentlyContinue | Where-Object { $$_.Path -eq '$R0\strelka.exe' } | Stop-Process -Force"`
    Pop $0
    nsExec::ExecToLog '"$R0\strelkad.exe" service stop'
    Pop $0
    ${If} $0 != 0
      MessageBox MB_ICONSTOP "Служба Astra не остановилась (код $0). Установка прервана, файлы не тронуты." /SD IDOK
      Abort
    ${EndIf}

    ; The window's autostart is a Run value named after the product.
    StrCpy $OldAutostart 0
    ReadRegStr $1 HKCU "${RUN_KEY}" "${OLD_PRODUCT}"
    ${If} $1 != ""
      StrCpy $OldAutostart 1
    ${EndIf}
    ReadRegStr $1 HKLM "${RUN_KEY}" "${OLD_PRODUCT}"
    ${If} $1 != ""
      StrCpy $OldAutostart 1
    ${EndIf}
    DeleteRegValue HKCU "${RUN_KEY}" "${OLD_PRODUCT}"
    DeleteRegValue HKLM "${RUN_KEY}" "${OLD_PRODUCT}"
    DeleteRegValue HKCU "${APPROVED_KEY}" "${OLD_PRODUCT}"
    DeleteRegValue HKLM "${APPROVED_KEY}" "${OLD_PRODUCT}"

    StrCpy $OldDesktop 0
    ${If} ${FileExists} "$DESKTOP\${OLD_PRODUCT}.lnk"
      StrCpy $OldDesktop 1
      !insertmacro UnpinShortcut "$DESKTOP\${OLD_PRODUCT}.lnk"
      Delete "$DESKTOP\${OLD_PRODUCT}.lnk"
    ${EndIf}
    !insertmacro UnpinShortcut "$SMPROGRAMS\${OLD_PRODUCT}.lnk"
    Delete "$SMPROGRAMS\${OLD_PRODUCT}.lnk"

    ; The files the old installer put there, by name: never a recursive
    ; delete of a folder read from the registry.
    Delete /REBOOTOK "$R0\strelka.exe"
    Delete /REBOOTOK "$R0\strelkad.exe"
    Delete /REBOOTOK "$R0\strelkactl.exe"
    Delete /REBOOTOK "$R0\uninstall.exe"
    RMDir /r /REBOOTOK "$R0\engines"
    RMDir /r /REBOOTOK "$R0\licenses"
    RMDir /REBOOTOK "$R0"

    DeleteRegKey HKLM "${OLD_UNINSTKEY}"
    DeleteRegKey HKLM "${OLD_MANUPRODUCTKEY}"
    DeleteRegKey /ifempty HKLM "Software\${OLD_PRODUCT}"
    DeleteRegValue HKCU "${OLD_MANUPRODUCTKEY}" "Installer Language"
    DeleteRegKey /ifempty HKCU "${OLD_MANUPRODUCTKEY}"
    DeleteRegKey /ifempty HKCU "Software\${OLD_PRODUCT}"
    StrCpy $FoldedStrelka 1
  ${EndIf}
!macroend

; An update does not create shortcuts, and the old ones are gone: they come
; back under the new name, and so does the window's autostart (the format
; tauri-plugin-autostart writes: the exe, a space, its arguments).
!macro ASTRA_RESTORE_STRELKA_ENTRIES
  ${If} $FoldedStrelka = 1
    CreateShortcut "$SMPROGRAMS\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    !insertmacro SetLnkAppUserModelId "$SMPROGRAMS\${PRODUCTNAME}.lnk"
    ${If} $OldDesktop = 1
      CreateShortcut "$DESKTOP\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
      !insertmacro SetLnkAppUserModelId "$DESKTOP\${PRODUCTNAME}.lnk"
    ${EndIf}
    ${If} $OldAutostart = 1
      WriteRegStr HKCU "${RUN_KEY}" "${PRODUCTNAME}" "$INSTDIR\${MAINBINARYNAME}.exe --hidden"
    ${EndIf}
  ${EndIf}
!macroend

!macro NSIS_HOOK_PREINSTALL
  !insertmacro STRELKA_ANSI_OUTPUT
  !insertmacro ASTRA_FOLD_IN_STRELKA
  ; An update or a reinstall over a running service: File would fail halfway
  ; on the locked strelkad.exe and leave a broken install.
  ${If} ${FileExists} "$INSTDIR\strelkad.exe"
    DetailPrint "Останавливаю службу Astra"
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service stop'
    Pop $0
    ${If} $0 != 0
      MessageBox MB_ICONSTOP "Служба Astra не остановилась (код $0). Установка прервана, файлы не тронуты." /SD IDOK
      Abort
    ${EndIf}
  ${EndIf}
!macroend

!macro NSIS_HOOK_POSTINSTALL
  !insertmacro STRELKA_ANSI_OUTPUT
  ; The same command for the first install and every update: install updates
  ; a service that already exists.
  DetailPrint "Регистрирую службу Astra"
  nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service install'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_ICONSTOP "Служба Astra не установилась (код $0). Без неё VPN не включится; подробности — в журнале установки." /SD IDOK
    Abort
  ${EndIf}
  DetailPrint "Запускаю службу Astra"
  nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service start'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_ICONEXCLAMATION "Служба Astra установлена, но не запустилась (код $0). Причина — в %ProgramData%\Strelka\logs\strelkad.log." /SD IDOK
  ${EndIf}
  !insertmacro ASTRA_RESTORE_STRELKA_ENTRIES
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  !insertmacro STRELKA_ANSI_OUTPUT
  ${If} $UpdateMode = 1
    ; Uninstalling on the way to a newer version: the service stays
    ; registered, only its files must be free.
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service stop'
    Pop $0
  ${Else}
    DetailPrint "Останавливаю и удаляю службу Astra"
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service stop'
    Pop $0
    ; Whatever filters are left - by a crash, or the boot-time guard - would
    ; keep the machine off the network with nothing left to lift them.
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" guard reset'
    Pop $0
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service uninstall'
    Pop $0
    ${If} $0 != 0
      MessageBox MB_ICONEXCLAMATION "Служба Astra не удалилась (код $0). Её можно удалить командой sc delete Strelka." /SD IDOK
    ${EndIf}
    ; sing-box with a TUN adds a Windows Firewall rule for its own exe, and
    ; the rule outlives the engine (seen: "sing-tun (<path>)" rules left from
    ; a TUN run days before). No such rule is an error here; it is ignored.
    nsExec::ExecToLog 'netsh advfirewall firewall delete rule name="sing-tun ($INSTDIR\engines\sing-box.exe)"'
    Pop $0
  ${EndIf}
!macroend

!macro NSIS_HOOK_POSTUNINSTALL
  ; The template removes the window's autostart from HKCU only. A window
  ; that was ever started as administrator registers itself in HKLM
  ; instead - seen in the stage 08 check, and left behind by the template.
  ${If} $UpdateMode <> 1
    DeleteRegValue HKLM "${RUN_KEY}" "${PRODUCTNAME}"
  ${EndIf}

  ; The template's "delete app data" box covers the window's folders; the
  ; service's data - subscriptions, keys, rules - lives in ProgramData. Kept
  ; unless the box is ticked, and always on an update or a silent uninstall.
  ${If} $DeleteAppDataCheckboxState = 1
  ${AndIf} $UpdateMode <> 1
    ReadEnvStr $1 ProgramData
    ${If} $1 != ""
      DetailPrint "Удаляю данные службы: $1\Strelka"
      RMDir /r "$1\Strelka"
    ${EndIf}
  ${EndIf}
!macroend
