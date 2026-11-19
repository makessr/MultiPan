package transfer

import (
	"context"
	"errors"
	"golang.org/x/time/rate"
	"io"
	"net/http"
)

type TransferSettings struct {
	UploadConcurrency     int  `json:"uploadConcurrency"`
	DownloadConcurrency   int  `json:"downloadConcurrency"`
	UploadKiB             int  `json:"uploadKiB"`
	DownloadKiB           int  `json:"downloadKiB"`
	CloseToTray           bool `json:"closeToTray"`
	AutomaticUpdateChecks bool `json:"automaticUpdateChecks,omitempty"`
}

func normalizeSettings(s TransferSettings) TransferSettings {
	if s.DownloadConcurrency < 1 || s.DownloadConcurrency > 8 {
		s.DownloadConcurrency = 1
	}
	if s.UploadConcurrency < 1 || s.UploadConcurrency > 8 {
		s.UploadConcurrency = 3
	}
	return s
}

type transferLimits struct{ up, down *rate.Limiter }

func newTransferLimits(s TransferSettings) *transferLimits {
	l := &transferLimits{rate.NewLimiter(rate.Inf, 128*1024), rate.NewLimiter(rate.Inf, 128*1024)}
	l.set(s)
	return l
}
func (l *transferLimits) set(s TransferSettings) {
	limit := func(v int) rate.Limit {
		if v <= 0 {
			return rate.Inf
		}
		return rate.Limit(v) * 1024
	}
	l.up.SetLimit(limit(s.UploadKiB))
	l.down.SetLimit(limit(s.DownloadKiB))
}

type limitContextKey struct{}

func withTransferLimits(ctx context.Context, l *transferLimits) context.Context {
	return context.WithValue(ctx, limitContextKey{}, l)
}

type limitedReader struct {
	io.ReadCloser
	ctx     context.Context
	limiter *rate.Limiter
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	n, e := r.ReadCloser.Read(p)
	if n > 0 {
		if err := r.limiter.WaitN(r.ctx, n); err != nil {
			return 0, err
		}
	}
	return n, e
}

type limitedTransport struct{ base http.RoundTripper }

func (t limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	l, _ := req.Context().Value(limitContextKey{}).(*transferLimits)
	if l == nil {
		return t.base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	if clone.Body != nil {
		clone.Body = &limitedReader{clone.Body, req.Context(), l.up}
	}
	resp, e := t.base.RoundTrip(clone)
	if e == nil && resp.Body != nil {
		resp.Body = &limitedReader{resp.Body, req.Context(), l.down}
	}
	return resp, e
}
func (m *Manager) SetTransferSettings(s TransferSettings) error {
	if s.UploadConcurrency < 1 || s.UploadConcurrency > 8 || s.DownloadConcurrency < 1 || s.DownloadConcurrency > 8 || s.UploadKiB < 0 || s.DownloadKiB < 0 || s.UploadKiB > 1048576 || s.DownloadKiB > 1048576 {
		return errors.New("并发范围1–8，限速范围0–1048576 KiB/s，0表示不限速")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.settings
	s.AutomaticUpdateChecks = old.AutomaticUpdateChecks
	m.settings = s
	if e := m.saveLocked(); e != nil {
		m.settings = old
		return e
	}
	if m.limits == nil {
		m.limits = newTransferLimits(s)
	} else {
		m.limits.set(s)
	}
	m.dispatchLocked()
	m.dispatchDownloadsLocked()
	return nil
}

func (m *Manager) AutomaticUpdateChecks() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closing && m.settings.AutomaticUpdateChecks
}

func (m *Manager) SetAutomaticUpdateChecks(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return errors.New("程序正在退出")
	}
	old := m.settings.AutomaticUpdateChecks
	if old == enabled {
		return nil
	}
	m.settings.AutomaticUpdateChecks = enabled
	if err := m.saveLocked(); err != nil {
		m.settings.AutomaticUpdateChecks = old
		return err
	}
	return nil
}
