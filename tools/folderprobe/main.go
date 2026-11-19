// Read-only verification: print counts, never credentials or private folder names.
package main

import (
	"context"
	"fmt"
	"multipan/internal/transfer"
	"os"
	"path/filepath"
	"time"
)

func main() {
	root, e := os.UserConfigDir()
	if e != nil {
		panic(e)
	}
	m, e := transfer.NewManager(filepath.Join(root, "MultiPan", "state.enc"), nil)
	if e != nil {
		panic("cannot read local account data")
	}
	count := 0
	for _, a := range m.Snapshot().Accounts {
		if a.Kind != "lanzou" {
			continue
		}
		count++
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		page, e := m.ListFolders(ctx, a.ID, "-1")
		cancel()
		if e != nil {
			fmt.Printf("account %d: root list failed: %v\n", count, e)
			continue
		}
		fmt.Printf("account %d: root list OK, %d folders\n", count, len(page.Folders))
		if len(page.Folders) > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
			child, e := m.ListFolders(ctx, a.ID, page.Folders[0].ID)
			cancel()
			if e != nil {
				fmt.Printf("account %d: child list failed: %v\n", count, e)
			} else {
				fmt.Printf("account %d: child list OK, %d folders, %d breadcrumbs\n", count, len(child.Folders), len(child.Breadcrumbs))
			}
		}
	}
	fmt.Printf("checked %d saved Lanzou accounts; no state writes or uploads\n", count)
}
