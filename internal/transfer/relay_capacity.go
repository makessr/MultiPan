package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const relayDiskMargin uint64 = 512 << 20

func relayJobTimeout(size int64) time.Duration {
	if size <= 256<<20 {
		return 30 * time.Minute
	}
	blocks := (size + (256 << 20) - 1) / (256 << 20)
	timeout := 30*time.Minute + time.Duration(blocks-1)*10*time.Minute
	if timeout > 6*time.Hour {
		return 6 * time.Hour
	}
	return timeout
}

func relaySizeLabel(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.2f GiB（%d 字节）", float64(n)/(1<<30), n)
	}
	return fmt.Sprintf("%.2f MiB（%d 字节）", float64(n)/(1<<20), n)
}
func relaySpaceBudget(available uint64, required int64) error {
	if required < 0 {
		return errors.New("中转缓存大小无效")
	}
	if uint64(required) > available || available-uint64(required) < relayDiskMargin {
		return fmt.Errorf("缓存磁盘可用 %.2f GiB，本次需要 %s，另需保留512 MiB；未开始传输", float64(available)/(1<<30), relaySizeLabel(required))
	}
	return nil
}
func relayCacheSpace(root string, required int64) error {
	if !filepath.IsAbs(root) {
		return errors.New("本机缓存路径无效，未开始传输")
	}
	probe := root
	for {
		info, e := os.Stat(probe)
		if e == nil {
			if !info.IsDir() {
				return errors.New("本机缓存上级不是目录，未开始传输")
			}
			break
		}
		if !os.IsNotExist(e) || filepath.Dir(probe) == probe {
			return errors.New("无法核对本机缓存磁盘空间，未开始传输")
		}
		probe = filepath.Dir(probe)
	}
	available, e := availableDiskBytes(probe)
	if e != nil {
		return errors.New("无法核对本机缓存磁盘空间，未开始传输")
	}
	return relaySpaceBudget(available, required)
}
