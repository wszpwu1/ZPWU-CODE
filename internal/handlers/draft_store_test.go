package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDraftStoreOrderingMoveToTopOnUpdate(t *testing.T) {
	d := newDraftStore("") // in-memory only
	a := d.save("alice", "o", "r", "main", "a.txt", "", "A", "c")
	b := d.save("alice", "o", "r", "main", "b.txt", "", "B", "c")
	c := d.save("alice", "o", "r", "main", "c.txt", "", "C", "c")

	// newest-first: c, b, a
	got := idsInOrder(d)
	if got[0] != c.ID || got[1] != b.ID || got[2] != a.ID {
		t.Fatalf("unexpected initial order: %v", got)
	}

	// Update "a" — should move to the top.
	updated := d.save("alice", "o", "r", "main", "a.txt", "", "A2", "c")
	if updated.ID != a.ID {
		t.Fatalf("update should reuse the same draft id (upsert), got %s vs %s", updated.ID, a.ID)
	}
	got = idsInOrder(d)
	if got[0] != a.ID || got[1] != c.ID || got[2] != b.ID {
		t.Fatalf("expected updated draft at front, got order %v (a=%s c=%s b=%s)", got, a.ID, c.ID, b.ID)
	}

	// Verify content actually updated.
	rec, ok := d.get(a.ID, "alice")
	if !ok || rec.Content != "A2" {
		t.Fatalf("expected content A2, got %v / %q", ok, rec.Content)
	}
}

func TestDraftStorePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drafts.json")

	d1 := newDraftStore(path)
	a := d1.save("alice", "o", "r", "main", "a.txt", "orig-a", "A", "cm-a")
	b := d1.save("bob", "o", "r", "main", "b.txt", "orig-b", "B", "cm-b")

	// Simulate a restart: build a fresh store from the same backing file.
	d2 := newDraftStore(path)

	if got := idsInOrder(d2); len(got) != 2 {
		t.Fatalf("expected 2 drafts after reload, got %d: %v", len(got), got)
	}
	recA, ok := d2.get(a.ID, "alice")
	if !ok {
		t.Fatalf("alice's draft missing after reload")
	}
	if recA.Content != "A" || recA.OriginalContent != "orig-a" || recA.CommitMessage != "cm-a" {
		t.Fatalf("alice's draft fields wrong after reload: %+v", recA)
	}
	recB, ok := d2.get(b.ID, "bob")
	if !ok || recB.Content != "B" {
		t.Fatalf("bob's draft missing/wrong after reload: %+v %v", recB, ok)
	}

	// Deleting on the reloaded store should also persist.
	if !d2.delete(a.ID, "alice") {
		t.Fatalf("delete returned false")
	}
	d3 := newDraftStore(path)
	if _, ok := d3.get(a.ID, "alice"); ok {
		t.Fatalf("deleted draft reappeared after second reload")
	}
	if _, ok := d3.get(b.ID, "bob"); !ok {
		t.Fatalf("surviving draft disappeared")
	}
}

func TestDraftStoreIgnoresCorruptSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drafts.json")
	// Write a garbage file at the expected path.
	if err := writeFileForTest(path, "this is not json {{{"); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	// Must not panic; must start empty.
	d := newDraftStore(path)
	if got := idsInOrder(d); len(got) != 0 {
		t.Fatalf("expected empty store after corrupt load, got %v", got)
	}
	// And still usable for saves.
	d.save("alice", "o", "r", "main", "x.txt", "", "X", "c")
	if got := idsInOrder(d); len(got) != 1 {
		t.Fatalf("expected 1 draft after save, got %d", len(got))
	}
}

// idsInOrder is a test helper that returns the current draft IDs top-to-bottom.
func idsInOrder(d *draftStore) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.order))
	for _, id := range d.order {
		if _, ok := d.items[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// writeFileForTest is a tiny helper so the test reads a bit cleaner.
func writeFileForTest(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
