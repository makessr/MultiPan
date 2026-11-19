package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"multipan/internal/transfer"
)

const uploadDropTarget = "upload-dropzone"

func (s *DesktopService) bindFileDrop(observer func(transfer.DropSelection)) {
	s.window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		details := event.Context().DropTargetDetails()
		if details == nil || (details.ElementID != uploadDropTarget && details.ElementID != "cloud-upload-dropzone") {
			return
		}
		selection := transfer.InspectDroppedUploadItems(event.Context().DroppedFiles())
		selection.TargetID = details.ElementID
		s.app.Event.Emit("multipan:files-dropped", selection)
		if observer != nil {
			observer(selection)
		}
	})
}
