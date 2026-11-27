package installation

import (
	"bytes"
	"crypto/ed25519"
	"debug/pe"
	"encoding/binary"
	"testing"
	"time"
)

func TestWindowsUpdateRejectsMixedArchitectures(t *testing.T) {
	now := time.Now()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			machine, otherMachine, otherArch := uint16(pe.IMAGE_FILE_MACHINE_AMD64), uint16(pe.IMAGE_FILE_MACHINE_ARM64), "arm64"
			if arch == "arm64" {
				machine, otherMachine, otherArch = otherMachine, machine, "amd64"
			}
			asset := updatePEFixture(machine)
			name := updateInstallerName("0.99.1", arch)
			raw, sig, err := SignUpdateManifest(bytes.NewReader(asset), name, "0.99.1", arch, now, time.Hour, private)
			if err != nil {
				t.Fatal(err)
			}
			verify := func(target string) (VerifiedUpdate, error) {
				return VerifyUpdateManifest(bytes.NewReader(raw), bytes.NewReader(sig), public, "0.99.0", target, now)
			}
			verified, err := verify(arch)
			if err != nil || verified.AssetName() != name {
				t.Fatal(verified, err)
			}
			if err := verified.VerifyInstaller(bytes.NewReader(asset)); err != nil {
				t.Fatal(err)
			}
			if err := verified.VerifyInstallerArchitecture(bytes.NewReader(asset)); err != nil {
				t.Fatal(err)
			}
			if err := verified.VerifyInstallerArchitecture(bytes.NewReader(updatePEFixture(otherMachine))); err == nil {
				t.Fatal("an installer for the other architecture was accepted")
			}
			if _, err := verify(otherArch); err == nil {
				t.Fatal("a signed manifest for the other architecture was accepted")
			}
			if _, _, err := SignUpdateManifest(bytes.NewReader(asset), name, "0.99.1", otherArch, now, time.Hour, private); err == nil {
				t.Fatal("mismatched installer name was signed")
			}
		})
	}
}

func updatePEFixture(machine uint16) []byte {
	dos := make([]byte, 64)
	copy(dos, "MZ")
	binary.LittleEndian.PutUint32(dos[0x3c:], 64)
	out := bytes.NewBuffer(dos)
	out.WriteString("PE\x00\x00")
	header := pe.OptionalHeader64{Magic: 0x20b, NumberOfRvaAndSizes: 16}
	_ = binary.Write(out, binary.LittleEndian, pe.FileHeader{
		Machine: machine, Characteristics: pe.IMAGE_FILE_EXECUTABLE_IMAGE, SizeOfOptionalHeader: uint16(binary.Size(header)),
	})
	_ = binary.Write(out, binary.LittleEndian, header)
	return out.Bytes()
}

func TestFirstInstallManifestVerifiesExactRelease(t *testing.T) {
	now := time.Now()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const version, arch = "0.99.0", "amd64"
	asset := updatePEFixture(pe.IMAGE_FILE_MACHINE_AMD64)
	name := updateInstallerName(version, arch)
	raw, sig, err := SignUpdateManifest(bytes.NewReader(asset), name, version, arch, now, time.Hour, private)
	if err != nil {
		t.Fatal(err)
	}
	release, err := VerifyFirstInstallManifest(bytes.NewReader(raw), bytes.NewReader(sig), public, version, arch, now)
	if err != nil || release.AssetName() != name || release.Version() != version {
		t.Fatal(release, err)
	}
	if err := release.VerifyInstaller(bytes.NewReader(asset)); err != nil {
		t.Fatal(err)
	}
	if err := release.VerifyInstaller(bytes.NewReader(append(asset, 0))); err == nil {
		t.Fatal("installer with bytes outside the pinned digest was accepted")
	}
	invalidSignature := append([]byte(nil), sig...)
	invalidSignature[0] ^= 1
	for _, test := range []struct {
		name, version, arch string
		sig                 []byte
		now                 time.Time
	}{
		{"different version", "0.98.0", arch, sig, now},
		{"different architecture", version, "arm64", sig, now},
		{"expired", version, arch, sig, now.Add(time.Hour)},
		{"bad signature", version, arch, invalidSignature, now},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := VerifyFirstInstallManifest(bytes.NewReader(raw), bytes.NewReader(test.sig), public, test.version, test.arch, test.now); err == nil {
				t.Fatal("untrusted release passed first-install verification")
			}
		})
	}
	if _, err := VerifyUpdateManifest(bytes.NewReader(raw), bytes.NewReader(sig), public, version, arch, now); err != ErrSameUpdateVersion {
		t.Fatal("update verification accepted a same-version install", err)
	}
}
