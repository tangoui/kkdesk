!include "LogicLib.nsh"

!macro customUnInstall
  ; Remove the user executable while we can still report a file-in-use error.
  ; The component command runs from the registered Program Files image.
  ClearErrors
  Delete "$INSTDIR\kkdesk.exe"
  IfErrors 0 kkdesk_check_component
  IfSilent kkdesk_uninstall_failed
  MessageBox MB_OK|MB_ICONEXCLAMATION "请先退出 kkdesk，再重新运行卸载程序。"
  kkdesk_uninstall_failed:
  SetErrorLevel 1
  Abort

  kkdesk_check_component:
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "com.kkdesk.desktop"
  IfSilent kkdesk_component_done
  ReadRegStr $0 HKLM "SYSTEM\CurrentControlSet\Services\KKDeskLockScreen" "ImagePath"
  StrCmp $0 "" kkdesk_component_done
  MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 \
    "要同时卸载 kkdesk 锁屏远控组件吗？Windows 会请求管理员批准。" \
    IDNO kkdesk_component_retained

  ; Do not elevate a binary from the user-writable installation directory.
  ; The service must point at the expected system copy.
  StrCmp $0 '"$PROGRAMFILES64\kkdesk\kkdesk.exe" -lockscreen-service' kkdesk_remove_component
  StrCmp $0 '$PROGRAMFILES64\kkdesk\kkdesk.exe -lockscreen-service' 0 kkdesk_component_retained

  kkdesk_remove_component:
  ; NSIS is a 32-bit process. Use SHELLEXECUTEINFOW with
  ; SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC to wait for the command.
  System::Call '*(i 60, i 0x140, p $HWNDPARENT, w "runas", w "$PROGRAMFILES64\kkdesk\kkdesk.exe", w "-uninstall-service", p 0, i 0, p 0, p 0, p 0, p 0, i 0, p 0, p 0) p .r1'
  System::Call 'shell32::ShellExecuteExW(p r1) i .r2'
  System::Call '*$1(i, i, p, p, p, p, p, i, p, p, p, p, i, p, p .r3)'
  System::Free $1
  StrCmp $2 0 kkdesk_component_retained
  StrCmp $3 0 kkdesk_component_retained
  System::Call 'kernel32::WaitForSingleObject(p r3, i -1) i .r2'
  StrCpy $4 1
  StrCmp $2 0 0 kkdesk_close_component
  System::Call 'kernel32::GetExitCodeProcess(p r3, *i .r4) i .r2'

  kkdesk_close_component:
  System::Call 'kernel32::CloseHandle(p r3)'
  StrCmp $2 0 0 kkdesk_component_retained
  StrCmp $4 0 kkdesk_component_done

  kkdesk_component_retained:
  MessageBox MB_OK|MB_ICONINFORMATION \
    "锁屏远控组件仍然保留。之后可以在 kkdesk「设置 › 安全」中卸载（需要管理员批准）。"

  kkdesk_component_done:
!macroend
