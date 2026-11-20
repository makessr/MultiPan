package desktop

import (
	"context"
	"multipan/internal/transfer"
	"time"
)

func (s *DesktopService) PendingReview() transfer.PendingReviewSnapshot {
	return s.manager.PendingReview()
}
func (s *DesktopService) CheckPendingReview(kind, id string) (transfer.PendingReviewOutcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.CheckPendingReview(ctx, kind, id)
}
