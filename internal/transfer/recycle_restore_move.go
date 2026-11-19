package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
)

const maxRestoredFileMoveBatch = 20

type RestoredFileMoveCheck struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

func restoredMoveTarget(source recycleRestoreSource, destination string) recycleRestoreSource {
	source.Entry.ParentID = destination
	if source.Purpose == "baidu-recycle-restore-v1" {
		source.Entry.OriginalPath = path.Join(destination, source.Entry.Name)
	}
	return source
}

// CheckConfirmedRestoredFileMove compares current locations without
// submitting a move or changing the original restore receipt.
func (m *Manager) CheckConfirmedRestoredFileMove(ctx context.Context, accountID, receiptID, destination string) (RestoredFileMoveCheck, error) {
	ids := []string{receiptID}
	a, inputs, err := m.restoredFileMoves(accountID, destination, ids, false)
	if err != nil {
		return RestoredFileMoveCheck{}, err
	}
	in := inputs[0]
	source := *in.restoredSource
	result := checkRestoredMoveLocations(ctx, NewHTTPUploader().Client, a, in, source)
	current, next, err := m.restoredFileMoves(accountID, destination, ids, false)
	if err != nil || downloadScope(a) != downloadScope(current) ||
		!sameRecycleRestoreEntry(source.Entry, next[0].restoredSource.Entry) {
		return RestoredFileMoveCheck{}, errors.New("核对期间账号或还原记录已变化")
	}
	return result, nil
}

func checkRestoredMoveLocations(ctx context.Context, client *http.Client, a Account, in MoveFileInput, source recycleRestoreSource) RestoredFileMoveCheck {
	folder := source.Entry.Kind == "folder"
	expectedID, objectID := "", ""
	label, identity := "文件", "文件身份和大小"
	if folder {
		label, identity = "文件夹", "文件夹身份"
		expectedID = restoredFolderID(source)
		if a.Kind == "baidu" {
			objectID = source.Entry.ID
		}
	} else if file := restoredFileForMove(source); file != nil {
		expectedID, objectID = file.ID, file.ObjectID
	}
	if expectedID == "" {
		return RestoredFileMoveCheck{State: "unknown", Message: "还原回执缺少可核对的项目标识"}
	}
	originMatch, originOccupied, originErr := recycleRestoreOriginal(ctx, client, a, source)
	if originErr != nil {
		return RestoredFileMoveCheck{State: "unknown", Message: "无法读取或核对原目录"}
	}
	targetMatch, _, targetErr := recycleRestoreOriginal(ctx, client, a, restoredMoveTarget(source, in.DestinationID))
	if targetErr != nil {
		return RestoredFileMoveCheck{State: "unknown", Message: "无法读取或核对所选目标目录"}
	}
	if originMatch && !targetMatch {
		return RestoredFileMoveCheck{State: "at_original", Message: label + "仍在原目录，所选目标未核对到此" + label}
	}
	if !originMatch && targetMatch && (!folder || !originOccupied) {
		original, err := ListAccountFiles(ctx, client, a, in.ParentID)
		if err == nil {
			for _, row := range original.Entries {
				if row.ID == expectedID || objectID != "" && row.ObjectID == objectID {
					return RestoredFileMoveCheck{State: "unknown", Message: "原目录仍存在同标识或同路径项目，需进一步核对"}
				}
			}
			return RestoredFileMoveCheck{State: "confirmed", Message: "所选目标的" + identity + "一致，原目录已无该" + label}
		}
	}
	return RestoredFileMoveCheck{State: "unknown", Message: "两处目录当前状态不足以确认" + label + "去向"}
}

func restoredFolderID(source recycleRestoreSource) string {
	e := source.Entry
	if source.DestinationID != "" || e.Kind != "folder" {
		return ""
	}
	switch source.Purpose {
	case "pan123-recycle-restore-v1", "guangya-recycle-restore-v1":
		return e.ID
	case "quark-recycle-restore-v1":
		return e.FileID
	case "baidu-recycle-restore-v1":
		return e.OriginalPath
	default:
		return ""
	}
}

func (m *Manager) MoveRestored123Folder(ctx context.Context, accountID, receiptID, destination string, sourceTrail, destinationTrail []CloudFolder) error {
	return m.moveRestoredFolder(ctx, accountID, receiptID, destination, sourceTrail, destinationTrail, true)
}

func (m *Manager) MoveConfirmedRestoredFolder(ctx context.Context, accountID, receiptID, destination string, sourceTrail, destinationTrail []CloudFolder) error {
	return m.moveRestoredFolder(ctx, accountID, receiptID, destination, sourceTrail, destinationTrail, false)
}

func (m *Manager) restoredFolderMoveSource(accountID, receiptID, destination string, require123 bool) (Account, recycleRestoreSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.downloadAccountLocked(accountID)
	op := m.recycleRestores[receiptID]
	if m.closing || a.ID == "" || op == nil || op.running || op.saved.Result.State != "confirmed" ||
		!recycleRestoreAccount(a, op.saved.Source) || require123 && a.Kind != "pan123" ||
		!validRestoredMoveDestination(a.Kind, destination) || restoredFolderID(op.saved.Source) == "" {
		return Account{}, recycleRestoreSource{}, errors.New("当前账号没有合适的已确认文件夹还原回执，或目标目录无效")
	}
	source := op.saved.Source
	if destination == recycleRestoreParent(source) || destination == restoredFolderID(source) {
		return Account{}, recycleRestoreSource{}, errors.New("请选择另一个目录")
	}
	return a, source, nil
}

func (m *Manager) CheckConfirmedRestoredFolderMove(ctx context.Context, accountID, receiptID, destination string) (RestoredFileMoveCheck, error) {
	a, source, err := m.restoredFolderMoveSource(accountID, receiptID, destination, false)
	if err != nil {
		return RestoredFileMoveCheck{}, err
	}
	in := MoveFileInput{ParentID: recycleRestoreParent(source), DestinationID: destination}
	result := checkRestoredMoveLocations(ctx, NewHTTPUploader().Client, a, in, source)
	_, current, err := m.restoredFolderMoveSource(accountID, receiptID, destination, false)
	if err != nil || current.Scope != source.Scope || !sameRecycleRestoreEntry(current.Entry, source.Entry) {
		return RestoredFileMoveCheck{}, errors.New("核对期间账号或还原回执已变化")
	}
	return result, nil
}

func (m *Manager) moveRestoredFolder(ctx context.Context, accountID, receiptID, destination string, sourceTrail, destinationTrail []CloudFolder, require123 bool) error {
	_, source, err := m.restoredFolderMoveSource(accountID, receiptID, destination, require123)
	if err != nil {
		return err
	}
	if _, err := m.LocateRecycleRestore(ctx, accountID, receiptID); err != nil {
		return err
	}
	_, err = m.moveEntry(ctx, MoveFileInput{
		AccountID: accountID, ParentID: recycleRestoreParent(source), EntryID: restoredFolderID(source),
		Name: source.Entry.Name, DestinationID: destination,
		SourceTrail: sourceTrail, DestinationTrail: destinationTrail,
		restoredSource: &source, restoreReceiptID: receiptID,
	}, true, source.Scope)
	return err
}

func validRestoredMoveDestination(kind, destination string) bool {
	switch kind {
	case "pan123":
		return valid123Folder(destination)
	case "quark":
		return validQuarkFolder(destination)
	case "guangya":
		return destination == guangyaRoot || destination != "0" && validQuarkFolder(destination)
	case "baidu":
		return validBaiduPath(destination)
	default:
		return false
	}
}

func (m *Manager) restoredFileMoves(accountID, destination string, ids []string, require123 bool) (Account, []MoveFileInput, error) {
	if len(ids) == 0 || len(ids) > maxRestoredFileMoveBatch {
		return Account{}, nil, errors.New("请选择 1 至 20 条已确认还原记录及有效目标目录")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.downloadAccountLocked(accountID)
	if m.closing || a.ID == "" || require123 && a.Kind != "pan123" || !validRestoredMoveDestination(a.Kind, destination) {
		return Account{}, nil, errors.New("当前账号或移动目标目录无效")
	}
	inputs := make([]MoveFileInput, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		op := m.recycleRestores[id]
		if op == nil || op.running || op.saved.Result.State != "confirmed" || !recycleRestoreAccount(a, op.saved.Source) {
			return Account{}, nil, errors.New("还原记录已变化或不属于当前账号")
		}
		source := op.saved.Source
		e := restoredFileForMove(source)
		if e == nil || seen[e.ID] {
			return Account{}, nil, errors.New("请选择不同的已确认文件还原记录")
		}
		parent := recycleRestoreParent(source)
		if parent == destination {
			return Account{}, nil, fmt.Errorf("「%s」已还原到所选目录，请移出本批后重试", e.Name)
		}
		for _, in := range inputs {
			if strings.EqualFold(in.Name, e.Name) {
				return Account{}, nil, fmt.Errorf("本批有同名文件「%s」，请分别处理", e.Name)
			}
		}
		seen[e.ID] = true
		inputs = append(inputs, MoveFileInput{AccountID: a.ID, ParentID: parent,
			DestinationID: destination, EntryID: e.ID, Name: e.Name, restoredSource: &source})
	}
	return a, inputs, nil
}

func (m *Manager) ValidateRestored123Moves(ctx context.Context, accountID, destination string, ids []string) error {
	return m.validateRestoredFileMoves(ctx, accountID, destination, ids, true)
}

func (m *Manager) ValidateRestoredFileMoves(ctx context.Context, accountID, destination string, ids []string) error {
	return m.validateRestoredFileMoves(ctx, accountID, destination, ids, false)
}

func (m *Manager) validateRestoredFileMoves(ctx context.Context, accountID, destination string, ids []string, require123 bool) error {
	a, inputs, err := m.restoredFileMoves(accountID, destination, ids, require123)
	if err != nil {
		return err
	}
	client := NewHTTPUploader().Client
	target, err := ListAccountFiles(ctx, client, a, destination)
	if err != nil {
		return errors.New("无法读取所选目标目录")
	}
	for _, in := range inputs {
		for _, entry := range target.Entries {
			if strings.EqualFold(entry.Name, in.Name) {
				return fmt.Errorf("目标目录已有同名项目「%s」", in.Name)
			}
		}
		match, _, err := recycleRestoreOriginal(ctx, client, a, *in.restoredSource)
		if err != nil || !match {
			return fmt.Errorf("无法在原目录核对「%s」", in.Name)
		}
	}
	current, next, err := m.restoredFileMoves(accountID, destination, ids, require123)
	if err != nil || downloadScope(a) != downloadScope(current) || len(next) != len(inputs) {
		return errors.New("还原记录或账号来源已变化，请重新选择")
	}
	for i, in := range inputs {
		if !sameRecycleRestoreEntry(in.restoredSource.Entry, next[i].restoredSource.Entry) {
			return errors.New("还原记录已变化，请重新选择")
		}
	}
	return nil
}

func (m *Manager) MoveRestored123File(ctx context.Context, accountID, receiptID, destination string) error {
	return m.moveConfirmedRestoredFile(ctx, accountID, receiptID, destination, true)
}

func (m *Manager) MoveConfirmedRestoredFile(ctx context.Context, accountID, receiptID, destination string) error {
	return m.moveConfirmedRestoredFile(ctx, accountID, receiptID, destination, false)
}

func (m *Manager) moveConfirmedRestoredFile(ctx context.Context, accountID, receiptID, destination string, require123 bool) error {
	a, inputs, err := m.restoredFileMoves(accountID, destination, []string{receiptID}, require123)
	if err != nil {
		return err
	}
	_, err = m.moveEntry(ctx, inputs[0], false, downloadScope(a))
	return err
}
