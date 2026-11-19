package transfer

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

func ListDAVFolders(ctx context.Context, client *http.Client, a Account, folder string) (FolderPage, error) {
	files, e := ListDAVFiles(ctx, client, a, folder)
	p := FolderPage{CurrentID: files.CurrentID, Folders: []CloudFolder{}, Breadcrumbs: files.Breadcrumbs}
	if e != nil {
		return p, e
	}
	for _, f := range files.Entries {
		if f.Folder {
			p.Folders = append(p.Folders, CloudFolder{ID: f.ID, Name: f.Name})
		}
	}
	return p, nil
}

func ListDAVFiles(ctx context.Context, client *http.Client, a Account, folder string) (FilePage, error) {
	return listDAVFiles(ctx, client, a, folder, false)
}

// A tree copy must fail instead of silently omitting inaccessible/invalid rows.
func listDAVFiles(ctx context.Context, client *http.Client, a Account, folder string, strict bool) (FilePage, error) {
	p := FilePage{CurrentID: folder, Entries: []CloudEntry{}, Breadcrumbs: []CloudFolder{{ID: "/", Name: "根目录"}}}
	if !validBaiduPath(folder) {
		return p, errors.New("WebDAV目录路径无效")
	}
	base, e := url.Parse(a.Endpoint)
	if e != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return p, errors.New("WebDAV地址须为HTTPS服务地址")
	}
	root := strings.TrimRight(base.Path, "/")
	base.Path = root + folder
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	base.RawPath = ""
	req, _ := http.NewRequestWithContext(ctx, "PROPFIND", base.String(), strings.NewReader(`<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/><d:getetag/><d:getlastmodified/></d:prop></d:propfind>`))
	req.SetBasicAuth(a.Username, a.Secret)
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	resp, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return p, ctx.Err()
		}
		return p, errors.New("WebDAV连接失败，请核对地址和网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 && resp.StatusCode != 200 {
		return p, statusError(resp.StatusCode)
	}
	var result struct {
		Responses []struct {
			Href  string `xml:"href"`
			Props []struct {
				Status string `xml:"status"`
				Prop   struct {
					Size     string `xml:"getcontentlength"`
					ETag     string `xml:"getetag"`
					Modified string `xml:"getlastmodified"`
					Resource *struct {
						Collection *struct{} `xml:"collection"`
					} `xml:"resourcetype"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if xml.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result) != nil {
		return p, errors.New("WebDAV目录响应无法识别，请核对服务地址")
	}
	seen := map[string]bool{}
	self := false
	for _, r := range result.Responses {
		href, e := url.Parse(r.Href)
		if e != nil {
			if strict {
				return p, errors.New("目录清单含无效路径，未创建中转任务")
			}
			continue
		}
		u := base.ResolveReference(href)
		if u.Scheme != base.Scheme || u.Host != base.Host || u.RawQuery != "" || u.Fragment != "" {
			if strict {
				return p, errors.New("目录清单含其他来源，未创建中转任务")
			}
			continue
		}
		clean := strings.TrimRight(u.Path, "/")
		if clean == "" {
			clean = "/"
		}
		collection, accessible, typed := false, false, false
		var size *int64
		revision := ""
		for _, pr := range r.Props {
			if strings.Contains(pr.Status, " 200 ") {
				accessible = true
				if pr.Prop.ETag != "" || pr.Prop.Modified != "" {
					revision = pr.Prop.ETag + "|" + pr.Prop.Modified
				}
				if pr.Prop.Resource != nil {
					typed = true
					if pr.Prop.Resource.Collection != nil {
						collection = true
					}
				}
				if length, e := strconv.ParseInt(strings.TrimSpace(pr.Prop.Size), 10, 64); e == nil && length >= 0 {
					size = &length
				}
			}
		}
		if !accessible {
			if strict {
				return p, errors.New("目录清单含不可读取项目，未创建中转任务")
			}
			continue
		}
		if strict && !typed {
			return p, errors.New("目录清单缺少项目类型，未创建中转任务")
		}
		current := strings.TrimRight(base.Path, "/")
		if current == "" {
			current = "/"
		}
		if clean == current {
			if strict && self {
				return p, errors.New("目录清单有重复项目")
			}
			self = collection
			continue
		}
		if path.Dir(clean) != current || !strings.HasPrefix(clean, root+"/") {
			if strict {
				return p, errors.New("目录清单含非直接子项，未创建中转任务")
			}
			continue
		}
		id := strings.TrimPrefix(clean, root)
		if !validBaiduPath(id) || seen[id] {
			if strict {
				return p, errors.New("目录清单有无效或重复项目")
			}
			continue
		}
		seen[id] = true
		p.Entries = append(p.Entries, CloudEntry{ID: id, Name: path.Base(id), Folder: collection, Size: size, revision: revision})
	}
	if !self {
		return p, errors.New("WebDAV未确认当前目录存在或可访问，请核对地址和权限")
	}
	current := ""
	if folder != "/" {
		for _, part := range strings.Split(strings.TrimPrefix(folder, "/"), "/") {
			current += "/" + part
			p.Breadcrumbs = append(p.Breadcrumbs, CloudFolder{ID: current, Name: part})
		}
	}
	return p, nil
}
