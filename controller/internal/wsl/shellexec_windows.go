package wsl

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SHELLEXECUTEINFOW per MSDN. Fields are listed in Win32 order; padding /
// alignment is handled by Go's struct layout for the chosen types.
type shellexecuteinfoW struct {
	CbSize         uint32
	FMask          uint32
	Hwnd           windows.Handle
	LpVerb         *uint16
	LpFile         *uint16
	LpParameters   *uint16
	LpDirectory    *uint16
	NShow          int32
	HInstApp       windows.Handle
	LpIDList       uintptr
	LpClass        *uint16
	HkeyClass      windows.Handle
	DwHotKey       uint32
	HIconOrMonitor windows.Handle
	HProcess       windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskFlagNoUI       = 0x00000400
	swHide                = 0
)

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteExW  = shell32.NewProc("ShellExecuteExW")
)

// shellExecuteHiddenPID launches `file params` via ShellExecuteExW with
// SW_HIDE and returns the spawned process's PID. The hProcess handle is
// closed before returning — only the PID is retained.
func shellExecuteHiddenPID(file, params, workDir string) (uint32, error) {
	fileP, _ := syscall.UTF16PtrFromString(file)
	paramsP, _ := syscall.UTF16PtrFromString(params)
	var dirP *uint16
	if workDir != "" {
		dirP, _ = syscall.UTF16PtrFromString(workDir)
	}

	info := shellexecuteinfoW{
		CbSize:       uint32(unsafe.Sizeof(shellexecuteinfoW{})),
		FMask:        seeMaskNoCloseProcess | seeMaskFlagNoUI,
		LpFile:       fileP,
		LpParameters: paramsP,
		LpDirectory:  dirP,
		NShow:        swHide,
	}
	r1, _, e := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		return 0, fmt.Errorf("ShellExecuteExW failed: %v", e)
	}
	if info.HProcess == 0 {
		return 0, fmt.Errorf("ShellExecuteExW returned no process handle")
	}
	pid, err := windows.GetProcessId(info.HProcess)
	_ = windows.CloseHandle(info.HProcess)
	if err != nil {
		return 0, err
	}
	return pid, nil
}
