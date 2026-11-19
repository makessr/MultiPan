package main

import (
	"fmt"
	"runtime"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// The user chooses a parent; the dedicated MultiPan subdirectory is appended
// by the caller and is shown again before any installation writes.
func chooseParentFolder() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); e != nil {
		if oe, ok := e.(*ole.OleError); !ok || oe.Code() != 1 {
			return "", e
		}
	}
	defer ole.CoUninitialize()
	obj, e := oleutil.CreateObject("Shell.Application")
	if e != nil {
		return "", e
	}
	defer obj.Release()
	shell, e := obj.QueryInterface(ole.IID_IDispatch)
	if e != nil {
		return "", e
	}
	defer shell.Release()
	chosen, e := oleutil.CallMethod(shell, "BrowseForFolder", 0, "选择安装父目录，程序将放在其中的 MultiPan 子目录", 0x51, 0)
	if e != nil {
		return "", e
	}
	defer chosen.Clear()
	if chosen.VT == ole.VT_EMPTY || chosen.VT == ole.VT_NULL || chosen.Val == 0 {
		return "", nil
	}
	if chosen.VT != ole.VT_DISPATCH {
		return "", fmt.Errorf("未取得文件系统目录")
	}
	item, e := oleutil.GetProperty(chosen.ToIDispatch(), "Self")
	if e != nil {
		return "", e
	}
	defer item.Clear()
	if item.VT != ole.VT_DISPATCH || item.Val == 0 {
		return "", fmt.Errorf("所选项目不是文件系统目录")
	}
	location, e := oleutil.GetProperty(item.ToIDispatch(), "Path")
	if e != nil {
		return "", e
	}
	defer location.Clear()
	if location.ToString() == "" {
		return "", fmt.Errorf("所选目录路径为空")
	}
	return location.ToString(), nil
}
