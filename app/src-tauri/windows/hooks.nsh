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

!macro NSIS_HOOK_PREINSTALL
  ; An update or a reinstall over a running service: File would fail halfway
  ; on the locked strelkad.exe and leave a broken install.
  ${If} ${FileExists} "$INSTDIR\strelkad.exe"
    DetailPrint "Останавливаю службу Strelka"
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service stop'
    Pop $0
    ${If} $0 != 0
      MessageBox MB_ICONSTOP "Служба Strelka не остановилась (код $0). Установка прервана, файлы не тронуты." /SD IDOK
      Abort
    ${EndIf}
  ${EndIf}
!macroend

!macro NSIS_HOOK_POSTINSTALL
  ; The same command for the first install and every update: install updates
  ; a service that already exists.
  DetailPrint "Регистрирую службу Strelka"
  nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service install'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_ICONSTOP "Служба Strelka не установилась (код $0). Без неё VPN не включится; подробности — в журнале установки." /SD IDOK
    Abort
  ${EndIf}
  DetailPrint "Запускаю службу Strelka"
  nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service start'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_ICONEXCLAMATION "Служба Strelka установлена, но не запустилась (код $0). Причина — в %ProgramData%\Strelka\logs\strelkad.log." /SD IDOK
  ${EndIf}
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  ${If} $UpdateMode = 1
    ; Uninstalling on the way to a newer version: the service stays
    ; registered, only its files must be free.
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service stop'
    Pop $0
  ${Else}
    DetailPrint "Останавливаю и удаляю службу Strelka"
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service stop'
    Pop $0
    ; Whatever filters are left - by a crash, or the boot-time guard - would
    ; keep the machine off the network with nothing left to lift them.
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" guard reset'
    Pop $0
    nsExec::ExecToLog '"$INSTDIR\strelkad.exe" service uninstall'
    Pop $0
    ${If} $0 != 0
      MessageBox MB_ICONEXCLAMATION "Служба Strelka не удалилась (код $0). Её можно удалить командой sc delete Strelka." /SD IDOK
    ${EndIf}
  ${EndIf}
!macroend

!macro NSIS_HOOK_POSTUNINSTALL
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
