package transfer

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRelayLargeMetadataQueueAndRestore(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			const large int64 = 600 << 20
			var m *Manager
			if directory {
				f, manager, in := newDirectoryFixture(t, "success")
				m = manager
				f.declaredSize = large
				m.renaming = map[string]bool{"s": true}
				if n, e := m.QueueRelayDirectory(context.Background(), in); e != nil || n != 5 {
					t.Fatal("600MiB directory was rejected", n, e)
				}
				f.mu.Lock()
				if f.creates != 0 || f.puts != 0 {
					t.Error("busy preflight wrote target")
				}
				f.mu.Unlock()
			} else {
				f, manager, in := newRelayFixture(t, "success")
				m = manager
				f.declaredSize = large
				in.Size = large
				m.renaming = map[string]bool{"source": true}
				if n, e := m.QueueRelays(context.Background(), []RelayInput{in}); e != nil || n != 1 {
					t.Fatal("600MiB file was rejected", n, e)
				}
				f.mu.Lock()
				if f.puts != 0 || f.gets != 0 {
					t.Error("metadata check transferred data")
				}
				f.mu.Unlock()
			}
			jobs := m.RelayJobs()
			count := 0
			for _, j := range jobs {
				if j.State != "paused" {
					t.Fatal("busy job started")
				}
				if !j.Folder {
					if j.Size != large {
						t.Fatal("large size truncated")
					}
					count++
				}
			}
			want := 1
			if directory {
				want = 2
			}
			if count != want {
				t.Fatal("file count changed")
			}
			statePath := m.path
			m.Close()
			restored, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal("large metadata restore", e)
			}
			defer restored.Close()
			for _, j := range restored.RelayJobs() {
				if !j.Folder && j.Size != large {
					t.Fatal("large persisted size lost")
				}
			}
		})
	}
}

func TestRelayCapacitySpaceAndDiagnostic(t *testing.T) {
	if relaySpaceBudget(uint64(600<<20)+relayDiskMargin, 600<<20) != nil {
		t.Fatal("exact margin refused")
	}
	if relaySpaceBudget(uint64(600<<20)+relayDiskMargin-1, 600<<20) == nil || relaySpaceBudget(1, 600<<20) == nil || relaySpaceBudget(0, -1) == nil {
		t.Fatal("unsafe cache space accepted")
	}
	if relayCacheSpace("relative-cache", 1) == nil {
		t.Fatal("relative cache volume accepted")
	}
	if relayJobTimeout(256<<20) != 30*time.Minute || relayJobTimeout(600<<20) <= 30*time.Minute || relayJobTimeout(relayMaxFile) > 6*time.Hour {
		t.Fatal("large file deadline not bounded")
	}
	f, m, in := newDirectoryFixture(t, "success")
	f.declaredSize = relayMaxFile + 1
	if _, e := m.QueueRelayDirectory(context.Background(), in); e == nil || !strings.Contains(e.Error(), "tree/file.txt") || !strings.Contains(e.Error(), "8 GiB") || !strings.Contains(e.Error(), "字节") {
		t.Fatal("oversize file not identified", e)
	}
	if len(m.RelayJobs()) != 0 || f.creates != 0 || f.puts != 0 {
		t.Fatal("oversize rejection changed target")
	}
}
