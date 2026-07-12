package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/savedra1/clipse/config"
	"github.com/savedra1/clipse/utils"
)

// point the global config at a temp sections file, isolated per test.
// the logger must be initialized too: utils.LogERROR calls log.Fatalf when no
// logger is set, which would kill the test process on the corrupt-file path.
// cmd.Main does the same via utils.SetUpLogger, so this mirrors the real app.
func setup(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	config.ClipseConfig.SectionsFilePath = filepath.Join(dir, "sections.json")
	utils.SetUpLogger(filepath.Join(dir, "clipse.log"))
}

func TestAddSection(t *testing.T) {
	setup(t)
	if err := config.AddSection("Emails"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	got := config.GetSections()
	if len(got) != 1 || got[0].Name != "Emails" {
		t.Fatalf("want 1 section named Emails, got %+v", got)
	}
	if got[0].Items == nil {
		t.Fatal("Items should be a non-nil empty slice")
	}
}

func TestAddSectionRejectsEmptyName(t *testing.T) {
	setup(t)
	if err := config.AddSection("   "); err == nil {
		t.Fatal("expected error for empty name")
	}
	if len(config.GetSections()) != 0 {
		t.Fatal("no section should have been written")
	}
}

func TestAddSectionRejectsDuplicateName(t *testing.T) {
	setup(t)
	if err := config.AddSection("Emails"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.AddSection("Emails"); err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if len(config.GetSections()) != 1 {
		t.Fatal("duplicate should not have been appended")
	}
}

func TestAddAndDeleteItem(t *testing.T) {
	setup(t)
	if err := config.AddSection("Emails"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.AddSectionItem("Emails", "a@b.com"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}

	items := config.GetSectionItems("Emails")
	if len(items) != 1 || items[0].Value != "a@b.com" {
		t.Fatalf("want 1 item a@b.com, got %+v", items)
	}

	if err := config.DeleteSectionItems("Emails", []string{items[0].Added}); err != nil {
		t.Fatalf("DeleteSectionItems: %v", err)
	}
	if len(config.GetSectionItems("Emails")) != 0 {
		t.Fatal("item should be gone")
	}
}

func TestAddItemToMissingSection(t *testing.T) {
	setup(t)
	if err := config.AddSectionItem("Nope", "x"); err == nil {
		t.Fatal("expected error adding to a section that does not exist")
	}
}

func TestDeleteSectionRemovesItsItems(t *testing.T) {
	setup(t)
	if err := config.AddSection("Emails"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.AddSectionItem("Emails", "a@b.com"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}
	if err := config.DeleteSection("Emails"); err != nil {
		t.Fatalf("DeleteSection: %v", err)
	}
	if len(config.GetSections()) != 0 {
		t.Fatal("section should be gone")
	}
	if len(config.GetSectionItems("Emails")) != 0 {
		t.Fatal("items of a deleted section should be gone")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	setup(t)
	if err := config.AddSection("Emails"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.AddSectionItem("Emails", "a@b.com"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}

	// GetSections re-reads from disk, so this proves it round-trips
	got := config.GetSections()
	if len(got) != 1 || len(got[0].Items) != 1 || got[0].Items[0].Value != "a@b.com" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got[0].Created == "" {
		t.Fatal("Created timestamp should be set")
	}
	if got[0].Items[0].Added == "" {
		t.Fatal("Added timestamp should be set")
	}
}

func TestCorruptFileYieldsEmptyAndDoesNotPanic(t *testing.T) {
	setup(t)
	if err := os.WriteFile(config.ClipseConfig.SectionsFilePath, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	got := config.GetSections() // must not panic
	if len(got) != 0 {
		t.Fatalf("corrupt file should yield no sections, got %+v", got)
	}

	// a corrupt file must be left alone, not silently clobbered
	raw, err := os.ReadFile(config.ClipseConfig.SectionsFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{not json" {
		t.Fatal("corrupt file was overwritten; it should be left intact until an explicit change")
	}
}

func TestMissingFileYieldsEmpty(t *testing.T) {
	setup(t) // file never created
	if len(config.GetSections()) != 0 {
		t.Fatal("missing file should yield no sections")
	}
}

func TestDuplicateValuesAllowedWithinSection(t *testing.T) {
	setup(t)
	if err := config.AddSection("S"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.AddSectionItem("S", "same"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}
	if err := config.AddSectionItem("S", "same"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}
	if len(config.GetSectionItems("S")) != 2 {
		t.Fatal("duplicate values within a section are allowed")
	}
}

func TestMaskSectionItem(t *testing.T) {
	setup(t)
	if err := config.AddSection("Creds"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.AddSectionItem("Creds", "hunter2"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}

	ts := config.GetSectionItems("Creds")[0].Added
	if err := config.MaskSectionItem("Creds", ts, "Entra PW"); err != nil {
		t.Fatalf("MaskSectionItem: %v", err)
	}

	got := config.GetSectionItems("Creds")[0]
	if !got.Masked {
		t.Fatal("item should be masked")
	}
	if got.Label != "Entra PW" {
		t.Fatalf("label = %q", got.Label)
	}
	// the whole point: the real value is still stored, so copying works
	if got.Value != "hunter2" {
		t.Fatalf("masking must not alter the stored value, got %q", got.Value)
	}
}

func TestMaskRequiresLabel(t *testing.T) {
	setup(t)
	if err := config.AddSection("Creds"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.AddSectionItem("Creds", "hunter2"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}

	ts := config.GetSectionItems("Creds")[0].Added
	if err := config.MaskSectionItem("Creds", ts, "  "); err == nil {
		t.Fatal("expected error: a masked item with no label would be unidentifiable")
	}
	if config.GetSectionItems("Creds")[0].Masked {
		t.Fatal("item should not have been masked")
	}
}

func TestMaskUnknownItem(t *testing.T) {
	setup(t)
	if err := config.AddSection("Creds"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.MaskSectionItem("Creds", "no-such-timestamp", "X"); err == nil {
		t.Fatal("expected error for unknown item")
	}
}

func TestSectionsAreIndependentOfEachOther(t *testing.T) {
	setup(t)
	for _, name := range []string{"A", "B"} {
		if err := config.AddSection(name); err != nil {
			t.Fatalf("AddSection(%s): %v", name, err)
		}
	}
	if err := config.AddSectionItem("A", "only-in-a"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}

	if len(config.GetSectionItems("A")) != 1 {
		t.Fatal("A should have its item")
	}
	if len(config.GetSectionItems("B")) != 0 {
		t.Fatal("B should not have picked up A's item")
	}

	if err := config.DeleteSection("A"); err != nil {
		t.Fatalf("DeleteSection: %v", err)
	}
	if len(config.GetSections()) != 1 || config.GetSections()[0].Name != "B" {
		t.Fatal("deleting A should leave B intact")
	}
}
