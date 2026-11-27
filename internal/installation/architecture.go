package installation

import (
	"debug/pe"
	"errors"
	"io"
)

// ExecutableArchitecture reads the machine type independently of the host.
func ExecutableArchitecture(input io.ReaderAt) (string, error) {
	file, err := pe.NewFile(input)
	if err == nil && file.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE != 0 && file.Characteristics&pe.IMAGE_FILE_DLL == 0 {
		if _, ok := file.OptionalHeader.(*pe.OptionalHeader64); ok {
			switch file.Machine {
			case pe.IMAGE_FILE_MACHINE_AMD64:
				return "amd64", nil
			case pe.IMAGE_FILE_MACHINE_ARM64:
				return "arm64", nil
			}
		}
	}
	return "", errors.New("需要 Windows x64 或 ARM64 可执行文件")
}

func InstallerPlatform(arch string) string {
	if arch == "amd64" {
		return "windows-x64"
	}
	return "windows-" + arch
}
