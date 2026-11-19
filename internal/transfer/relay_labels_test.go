package transfer

import "testing"

func TestRelayHistoryProviderSnapshots(t *testing.T) {
	m := &Manager{accounts: []Account{{ID: "s", Kind: "quark", Name: "renamed"}, {ID: "t", Kind: "webdav", Name: "renamed"}}, relays: []diskRelay{{SourceKind: "pan123", TargetKind: "baidu", Input: RelayInput{SourceAccountID: "s", TargetAccountID: "t"}, Job: RelayJob{SourceName: "same saved name", TargetName: "same saved name"}}, {Job: RelayJob{SourceName: "legacy source", TargetName: "legacy target"}}}}
	rows := m.RelayJobs()
	if len(rows) != 2 || rows[0].SourceKind != "pan123" || rows[0].TargetKind != "baidu" || rows[0].SourceName != "same saved name" || rows[0].TargetName != "same saved name" {
		t.Fatal("history guessed current account", rows)
	}
	if rows[1].SourceKind != "legacy-dav" || rows[1].TargetKind != "legacy-dav" {
		t.Fatal("legacy types guessed", rows[1])
	}
	if m.relays[0].Job.SourceKind != "" || m.relays[1].SourceKind != "" {
		t.Fatal("display mutated saved snapshot")
	}
}
