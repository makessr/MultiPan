package installation

import (
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var retrieveSIPSubject = windows.NewLazySystemDLL("crypt32.dll").NewProc("CryptSIPRetrieveSubjectGuid")

func verifyWindowsAuthenticode(path *uint16, file windows.Handle, subject *windows.GUID, flags uint32) error {
	info := windows.WinTrustFileInfo{
		Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: path,
		File: file, KnownSubject: subject,
	}
	data := windows.WinTrustData{
		Size: uint32(unsafe.Sizeof(windows.WinTrustData{})), UIChoice: windows.WTD_UI_NONE,
		UnionChoice: windows.WTD_CHOICE_FILE, StateAction: windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&info),
		ProvFlags:                       flags | windows.WTD_REVOCATION_CHECK_CHAIN_EXCLUDE_ROOT | windows.WTD_DISABLE_MD2_MD4,
	}
	action := windows.WINTRUST_ACTION_GENERIC_VERIFY_V2
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &action, &data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	closeErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &action, &data)
	if verifyErr != nil {
		return verifyErr
	}
	return closeErr
}

// OpenSignedUpdateInstaller keeps the checked file open and blocks writes
// until the caller finishes hashing the same bytes for the release manifest.
func OpenSignedUpdateInstaller(path string) (*os.File, error) {
	file, err := openUpdateInstaller(path)
	if err != nil {
		return nil, err
	}
	if err := verifyUpdateAuthenticode(file, 0); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func openUpdateInstaller(path string) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errors.New("无法以只读方式锁定待发布的安装包")
	}
	file := os.NewFile(uintptr(handle), path)
	if _, err := updateInstallerSize(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func verifyUpdateAuthenticode(file *os.File, flags uint32) error {
	size, err := updateInstallerSize(file)
	if err != nil {
		return err
	}
	handle := windows.Handle(file.Fd())
	installer, err := pe.NewFile(file)
	if err != nil || (installer.Machine != pe.IMAGE_FILE_MACHINE_AMD64 && installer.Machine != pe.IMAGE_FILE_MACHINE_ARM64) ||
		installer.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || installer.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return errors.New("更新安装包必须为 Windows x64 或 ARM64 可执行文件")
	}
	header, ok := installer.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || header.NumberOfRvaAndSizes <= pe.IMAGE_DIRECTORY_ENTRY_SECURITY {
		return errors.New("Windows 安装包缺少内嵌 Authenticode 签名")
	}
	certificate := header.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_SECURITY]
	// Unlike other PE directories, the security directory uses a file offset.
	if certificate.VirtualAddress == 0 || certificate.Size < 8 || uint64(certificate.VirtualAddress)+uint64(certificate.Size) > size {
		return errors.New("Windows 安装包的内嵌签名缺失或不完整")
	}
	name, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// The staged file has a .partial suffix. Resolve its subject from the
	// open file, so verification does not need an executable path.
	var subject windows.GUID
	if err := retrieveSIPSubject.Find(); err != nil {
		return errors.New("Windows 无法加载安装包验签组件")
	}
	okSIP, _, err := retrieveSIPSubject.Call(uintptr(unsafe.Pointer(name)), uintptr(handle), uintptr(unsafe.Pointer(&subject)))
	if okSIP == 0 {
		return fmt.Errorf("Windows 无法识别安装包签名格式：%w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := verifyWindowsAuthenticode(name, handle, &subject, flags); err != nil {
		return fmt.Errorf("更新安装包 Authenticode 验签未通过：%w", err)
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}

func updateInstallerSize(file *os.File) (uint64, error) {
	var details windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &details); err != nil ||
		details.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return 0, errors.New("更新验签仅接受普通安装包文件")
	}
	size := uint64(details.FileSizeHigh)<<32 | uint64(details.FileSizeLow)
	if size == 0 || size > maxUpdateInstaller {
		return 0, errors.New("安装包为空或超过更新大小限制")
	}
	return size, nil
}
