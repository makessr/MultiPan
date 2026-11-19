package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var downloadJobIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func downloadPartialPaths(j DownloadJob) ([]string, error) {
	if !downloadJobIDPattern.MatchString(j.ID) || !filepath.IsAbs(j.Destination) {
		return nil, errors.New("任务的临时文件标识无效")
	}
	part := j.Destination + ".multipan-" + j.ID + ".part"
	paths := []string{part, part + ".resume"}
	for _, p := range paths {
		info, e := os.Lstat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, errors.New("无法检查任务临时文件")
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("临时路径已被替换为目录或链接，未清理")
		}
	}
	return paths, nil
}

func (m *Manager) ClearDownloadPartials(ids []string) ([]DownloadActionResult, error) {
	if len(ids) == 0 || len(ids) > 200 {
		return nil, errors.New("每次选择1–200个下载任务")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, errors.New("程序正在退出")
	}
	results := []DownloadActionResult{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		r := DownloadActionResult{ID: id, Message: "任务不存在"}
		for i := range m.downloads {
			j := &m.downloads[i]
			if j.ID != id {
				continue
			}
			r.Name = j.Input.Name
			if _, active := m.downloadActive[id]; active || (j.Status != "paused" && j.Status != "failed") {
				r.Message = "仅可清理已停止的暂停或失败任务"
				break
			}
			paths, e := downloadPartialPaths(*j)
			if e != nil {
				r.Message = e.Error()
				break
			}
			for _, p := range paths {
				if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
					break
				}
				e = nil
			}
			if e != nil {
				r.Message = "临时文件清理未完成，可能被其他程序占用；正式文件未删除"
				break
			}
			j.Received = 0
			j.Message = "续传临时文件已清理，继续时从头下载"
			r.Applied = true
			r.Message = j.Message
			break
		}
		results = append(results, r)
	}
	if e := m.saveLocked(); e != nil {
		return results, errors.New("临时文件可能已清理，但记录保存失败；请检查磁盘，正式文件未删除")
	}
	return results, nil
}

func (m *Manager) DownloadReport(ids []string, version string) (string, error) {
	if len(ids) == 0 || len(ids) > 200 {
		return "", errors.New("每次选择1–200个下载任务")
	}
	if len(version) > 32 || !regexp.MustCompile("^[0-9]+\\.[0-9]+\\.[0-9]+$").MatchString(version) {
		return "", errors.New("程序版本无效")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	report := "MultiPan " + version + " 下载结果报告\r\n\r\n"
	names := map[string]string{"lanzou": "蓝奏云", "quark": "夸克网盘", "pan123": "123 云盘", "baidu": "百度网盘", "guangya": "光鸭云盘", "ctdav": "诚通网盘", "webdav": "WebDAV"}
	statuses := map[string]string{"queued": "等待中", "running": "下载中", "paused": "已暂停", "failed": "失败", "saved": "已保存"}
	count := 0
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, j := range m.downloads {
			if j.ID != id {
				continue
			}
			kind := names[j.Kind]
			if kind == "" {
				kind = j.Kind
			}
			status := statuses[j.Status]
			if status == "" {
				status = j.Status
			}
			report += fmt.Sprintf("文件：%s\r\n网盘：%s\r\n保存位置：%s\r\n状态：%s\r\n字节：%d / %d\r\n说明：%s\r\n校验：%s\r\n\r\n", j.Input.Name, kind, j.Destination, status, j.Received, j.Total, j.Message, reportVerification(j))
			count++
			break
		}
		if len(report) > 2<<20 {
			return "", errors.New("报告超过2 MiB，请减少选择项目")
		}
	}
	if count == 0 {
		return "", errors.New("所选下载记录已失效")
	}
	return report, nil
}
func reportVerification(j DownloadJob) string {
	label := "未完成，未验证"
	if j.Status == "saved" {
		if j.Result.Verification == "verified" {
			label = "校验通过"
		} else {
			label = "已保存，完整性未验证"
		}
	}
	if j.Result.Details != "" {
		label += " · " + j.Result.Details
	}
	return label
}
