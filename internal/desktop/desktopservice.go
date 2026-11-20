package desktop

import (
	"context"
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"multipan/internal/transfer"
	"sync"
	"time"
)

type DesktopService struct {
	resources         Resources
	loginMu           sync.Mutex
	loginCancel       context.CancelFunc
	downloadMu        sync.Mutex
	downloadCancel    context.CancelFunc
	downloadPathMu    sync.Mutex
	downloadPaths     map[string]bool
	downloadPathOrder []string
	updates           applicationUpdates
	manager           *transfer.Manager
	app               *application.App
	window            *application.WebviewWindow
}

func (s *DesktopService) Snapshot() transfer.Snapshot { return s.manager.Snapshot() }
func (s *DesktopService) PickFiles() ([]transfer.File, error) {
	paths, e := s.app.Dialog.OpenFile().CanChooseFiles(true).CanChooseDirectories(false).AttachToWindow(s.window).PromptForMultipleSelection()
	if e != nil {
		return nil, e
	}
	result := []transfer.File{}
	for _, path := range paths {
		f, e := transfer.Inspect(path)
		if e != nil {
			return nil, fmt.Errorf("无法读取文件：%s", path)
		}
		result = append(result, f)
	}
	return result, nil
}
func (s *DesktopService) SaveAccount(in transfer.AccountInput) (transfer.Account, error) {
	if in.Kind == "guangya" && in.Secret != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if e := transfer.VerifyGuangyaSession(ctx, transfer.NewHTTPUploader().Client, transfer.Account{Endpoint: transfer.GuangyaAPI, Secret: in.Secret, Username: in.Username, UserAgent: in.UserAgent}); e != nil {
			return transfer.Account{}, e
		}
	}
	if in.Kind == "ctdav" && in.Secret != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		folder := in.Folder
		if folder == "" {
			folder = "/"
		}
		if _, e := transfer.ListDAVFolders(ctx, transfer.NewHTTPUploader().Client, transfer.Account{Kind: "ctdav", Endpoint: in.Endpoint, Username: in.Username, Secret: in.Secret}, folder); e != nil {
			return transfer.Account{}, e
		}
	}
	if in.Kind == "ctfile" && in.Secret != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if e := transfer.VerifyCtSession(ctx, transfer.NewHTTPUploader().Client, transfer.Account{Endpoint: transfer.CtFileAPI, Secret: in.Secret}); e != nil {
			return transfer.Account{}, e
		}
	}
	if in.Kind == "baidu" && in.Secret != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		fresh, e := transfer.CheckBaiduSession(ctx, transfer.NewHTTPUploader().Client, transfer.Account{Kind: "baidu", Endpoint: transfer.BaiduAPI, Secret: in.Secret, UserAgent: in.UserAgent})
		if e != nil {
			return transfer.Account{}, e
		}
		in.Secret = fresh
	}
	if in.Kind == "pan123" && in.Secret != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if e := transfer.Verify123Session(ctx, transfer.NewHTTPUploader().Client, transfer.Account{Kind: "pan123", Endpoint: transfer.Pan123API, Secret: in.Secret, UserAgent: in.UserAgent}); e != nil {
			return transfer.Account{}, e
		}
	}
	if in.Kind == "quark" && in.Secret != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		fresh, e := transfer.CheckQuarkSession(ctx, transfer.NewHTTPUploader().Client, transfer.Account{Endpoint: transfer.QuarkAPI, Kind: "quark", Secret: in.Secret, UserAgent: in.UserAgent})
		if e != nil {
			return transfer.Account{}, e
		}
		in.Secret = fresh
	}
	return s.manager.SaveAccount(in)
}
func (s *DesktopService) RequestCtSMS(phone string) (transfer.CtSMSChallenge, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return transfer.RequestCtSMS(ctx, transfer.NewHTTPUploader().Client, phone)
}
func (s *DesktopService) LoginCtFile(in transfer.CtLoginInput) (transfer.Account, error) {
	if in.Name == "" {
		return transfer.Account{}, fmt.Errorf("请填写账号名称")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, e := transfer.LoginCtSession(ctx, transfer.NewHTTPUploader().Client, in)
	if e != nil {
		return transfer.Account{}, e
	}
	return s.manager.SaveAccount(transfer.AccountInput{ID: in.ID, Name: in.Name, Kind: "ctfile", Folder: in.Folder, Username: in.Space, Secret: token})
}
func (s *DesktopService) DeleteAccount(id string) error { return s.manager.DeleteAccount(id) }
func (s *DesktopService) Enqueue(files []transfer.File, targets []transfer.Target) (int, error) {
	return s.manager.Enqueue(files, targets)
}
func (s *DesktopService) Stop(id string) error  { return s.manager.Stop(id) }
func (s *DesktopService) Retry(id string) error { return s.manager.Retry(id) }
func (s *DesktopService) ListCloudFolders(id, parent string) (transfer.FolderPage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return s.manager.ListFolders(ctx, id, parent)
}
func (s *DesktopService) SetDefaultFolder(id, folder, display string) error {
	return s.manager.SetDefaultFolder(id, folder, display)
}

func (s *DesktopService) ListCloudFiles(id, parent string) (transfer.FilePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.ListFiles(ctx, id, parent)
}

func (s *DesktopService) CreateCloudFolder(id, parent, name string) (transfer.CloudFolder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.CreateFolder(ctx, id, parent, name)
}

func (s *DesktopService) RenameCloudEntry(in transfer.RenameInput) (transfer.CloudEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.manager.RenameEntry(ctx, in)
}

func (s *DesktopService) MoveCloudFile(in transfer.MoveFileInput) (transfer.CloudEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.MoveFile(ctx, in)
}

func (s *DesktopService) MoveCloudFolder(in transfer.MoveFileInput) (transfer.CloudEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.MoveFolder(ctx, in)
}

func (s *DesktopService) ValidateCloudMoveBatch(entries []transfer.MoveBatchEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidateMoveBatch(ctx, entries)
}

func (s *DesktopService) CopyCloudEntry(entry transfer.MoveBatchEntry) (transfer.CloudEntry, error) {
	return s.manager.CopyEntry(context.Background(), entry)
}

func (s *DesktopService) CancelCloudCopy(accountID, entryID, destinationID string) bool {
	return s.manager.CancelCopy(accountID, entryID, destinationID)
}

func (s *DesktopService) CopyPan123FileToDirectory(entry transfer.MoveBatchEntry) (transfer.CloudEntry, error) {
	return s.manager.CopyPan123FileToDirectory(context.Background(), entry)
}

func (s *DesktopService) CopyPan123FolderToDirectory(entry transfer.MoveBatchEntry) (transfer.CloudEntry, error) {
	return s.manager.CopyPan123FolderToDirectory(context.Background(), entry)
}

func (s *DesktopService) CopyGuangyaFolderToDirectory(entry transfer.MoveBatchEntry) (transfer.CloudEntry, error) {
	return s.manager.CopyGuangyaFolderToDirectory(context.Background(), entry)
}

func (s *DesktopService) ValidateGuangyaDirectCopyBatch(entries []transfer.MoveBatchEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidateGuangyaDirectCopyBatch(ctx, entries)
}

func (s *DesktopService) ValidatePan123CopyBatch(entries []transfer.MoveBatchEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidatePan123CopyBatch(ctx, entries)
}

func (s *DesktopService) ValidatePan123DirectCopyBatch(entries []transfer.MoveBatchEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidatePan123DirectCopyBatch(ctx, entries)
}

func (s *DesktopService) ValidateGuangyaCopyBatch(entries []transfer.MoveBatchEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.manager.ValidateGuangyaCopyBatch(ctx, entries)
}

func (s *DesktopService) ValidateLanzouCopyBatch(entries []transfer.MoveBatchEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return s.manager.ValidateLanzouCopyBatch(ctx, entries)
}

func (s *DesktopService) CloudCopyHistory(accountID string) []transfer.CopyReceipt {
	return s.manager.CopyHistory(accountID)
}
func (s *DesktopService) ClearCloudCopyHistory(accountID string) (int, error) {
	return s.manager.ClearCopyHistory(accountID)
}
func (s *DesktopService) CheckCloudCopy(id string) (transfer.CopyReceipt, error) {
	return s.manager.CheckCopy(context.Background(), id)
}

func (s *DesktopService) LocateCloudCopyDestination(accountID, receiptID string) (transfer.CopyDestinationLocation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.LocateCopyDestination(ctx, accountID, receiptID)
}

func (s *DesktopService) LocateCloudCopyResult(accountID, receiptID string) (transfer.CopyDestinationLocation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.LocateCopyResult(ctx, accountID, receiptID)
}

func (s *DesktopService) OpenCtAuthorization() error {
	return s.app.Browser.OpenURL("https://home.ctfile.com/settings/openapi")
}

func (s *DesktopService) OpenCtWebDAV() error {
	return s.app.Browser.OpenURL("https://home.ctfile.com/settings/webdav")
}
