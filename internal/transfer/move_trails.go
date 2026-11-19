package transfer

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type folderMoveLabels struct{ Old, New string }

func validIDFolder(kind, id string) bool {
	if kind == "quark" || kind == "guangya" {
		return validQuarkFolder(id)
	}
	return valid123Folder(id)
}

// Trails are supplied by navigation, but every ID edge is checked against a
// fresh server listing. Names used in saved labels come only from that listing.
func verifiedMoveTrails(ctx context.Context, client *http.Client, a Account, in MoveFileInput) (func(string) (FilePage, error), string, string, error) {
	cache := map[string]FilePage{}
	list := func(id string) (FilePage, error) {
		if p, ok := cache[id]; ok {
			return p, nil
		}
		p, e := ListAccountFiles(ctx, client, a, id)
		if e == nil {
			cache[id] = p
		}
		return p, e
	}
	verify := func(trail []CloudFolder, expected string, checkDescendant bool) (string, error) {
		invalid := errors.New("目录层级无法确认，请返回根目录重新浏览后移动；未发送移动请求 [MOVE-TRAIL/0.18.0]")
		if len(trail) == 0 || len(trail) > 64 || trail[0].ID != cloudRoot(a.Kind) || trail[len(trail)-1].ID != expected {
			return "", invalid
		}
		seen := map[string]bool{}
		names := []string{"根目录"}
		for i, step := range trail {
			if !validIDFolder(a.Kind, step.ID) || seen[step.ID] {
				return "", invalid
			}
			seen[step.ID] = true
			if checkDescendant && step.ID == in.EntryID {
				return "", errors.New("不能将文件夹移入自身或其子目录")
			}
			if i == 0 {
				continue
			}
			p, e := list(trail[i-1].ID)
			if e != nil {
				return "", e
			}
			count := 0
			name := ""
			for _, entry := range p.Entries {
				if entry.ID == step.ID && entry.Folder {
					count++
					name = entry.Name
				}
			}
			if count != 1 || name == "" {
				return "", invalid
			}
			names = append(names, name)
		}
		return strings.Join(names, " / "), nil
	}
	old, e := verify(in.SourceTrail, in.ParentID, true)
	if e != nil {
		return nil, "", "", e
	}
	next, e := verify(in.DestinationTrail, in.DestinationID, true)
	if e != nil {
		return nil, "", "", e
	}
	return list, old, next, nil
}

// ID destinations remain valid after native moves. Only their displayed paths
// change. Completed history and labels outside the verified prefix stay intact.
func (m *Manager) updateMovedIDFolderLocked(in MoveFileInput, labels folderMoveLabels) bool {
	if labels.Old == "" || labels.New == "" {
		return false
	}
	changed := false
	update := func(id string, label *string) {
		old := *label
		if id == in.EntryID {
			*label = labels.New
		} else {
			*label = renamedDisplay(*label, labels.Old, labels.New)
		}
		changed = changed || old != *label
	}
	for i := range m.accounts {
		if m.accounts[i].ID == in.AccountID {
			update(m.accounts[i].Folder, &m.accounts[i].FolderDisplay)
		}
	}
	for i := range m.tasks {
		t := &m.tasks[i]
		if t.AccountID == in.AccountID && (t.Status == "paused" || t.Status == "failed") {
			update(t.Folder, &t.FolderDisplay)
		}
	}
	return changed
}
