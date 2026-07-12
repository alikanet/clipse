package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/savedra1/clipse/config"
	"github.com/savedra1/clipse/utils"
)

// A masked item must show its label on screen while still carrying the real
// value in titleFull, because that is the field every copy path reads
// (handleChooseOperation and the sections copy both use it). If masking ever
// overwrote titleFull, copying a saved password would paste asterisks.
func TestMaskedItemHidesValueButCopiesIt(t *testing.T) {
	dir := t.TempDir()
	config.ClipseConfig.SectionsFilePath = filepath.Join(dir, "sections.json")
	config.ClipseConfig.MaxEntryLength = 65
	utils.SetUpLogger(filepath.Join(dir, "clipse.log"))

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

	items := sectionItemListItems("Creds")
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}

	i, ok := items[0].(item)
	if !ok {
		t.Fatal("not an item")
	}

	if i.title != "Entra PW" {
		t.Fatalf("the label should be what is shown, got %q", i.title)
	}
	if strings.Contains(i.title, "hunter2") || strings.Contains(i.description, "hunter2") {
		t.Fatalf("the secret must not appear on screen: title=%q desc=%q", i.title, i.description)
	}
	if i.description != strings.Repeat(maskChar, len("hunter2")) {
		t.Fatalf("want one mask char per character, got %q", i.description)
	}

	// the copy path reads titleFull -- it must still be the real value
	if i.titleFull != "hunter2" {
		t.Fatalf("copying must yield the real value, got %q", i.titleFull)
	}

	// the filter matches on FilterValue, which is the title: the secret is not
	// searchable
	if i.FilterValue() != "Entra PW" {
		t.Fatalf("the secret must not be searchable, FilterValue=%q", i.FilterValue())
	}
}

func TestUnmaskedItemIsUnaffected(t *testing.T) {
	dir := t.TempDir()
	config.ClipseConfig.SectionsFilePath = filepath.Join(dir, "sections.json")
	config.ClipseConfig.MaxEntryLength = 65
	utils.SetUpLogger(filepath.Join(dir, "clipse.log"))

	if err := config.AddSection("S"); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	if err := config.AddSectionItem("S", "plain value"); err != nil {
		t.Fatalf("AddSectionItem: %v", err)
	}

	i, ok := sectionItemListItems("S")[0].(item)
	if !ok {
		t.Fatal("not an item")
	}
	if i.masked {
		t.Fatal("should not be masked")
	}
	if i.title != "plain value" || i.titleFull != "plain value" {
		t.Fatalf("unmasked item should show its value, got %q", i.title)
	}
	if !strings.HasPrefix(i.description, "Date added:") {
		t.Fatalf("unmasked item should show its date, got %q", i.description)
	}
}

func TestMaskOfIsCapped(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := maskOf(long)
	if len(got) != maskMaxChars {
		t.Fatalf("a long secret should be capped at %d chars, got %d", maskMaxChars, len(got))
	}
	if strings.Contains(got, "x") {
		t.Fatal("mask leaked the value")
	}
}
