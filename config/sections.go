package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/savedra1/clipse/utils"
)

/* Storage for user-defined sections: named groups of saved text snippets.

Kept in its own file rather than in clipboard_history.json because the clipboard
listener rewrites the history file in full on every copy event, with no locking.
Sections are written only by the TUI, so curated data is never in that race.

Text only. Section items hold no filePath and no link back to the history entry
they were imported from: the history evicts entries at maxHistory and deletes
them, so a linked item would die with it.
*/

type SectionItem struct {
	Value  string `json:"value"`
	Added  string `json:"added"`            // timestamp, doubles as the item ID
	Label  string `json:"label,omitempty"`  // shown in place of the value when masked
	Masked bool   `json:"masked,omitempty"` // hide the value on screen; copying is unaffected
}

type Section struct {
	Name    string        `json:"name"` // unique, the section ID
	Created string        `json:"created"`
	Items   []SectionItem `json:"items"`
}

type Sections struct {
	Sections []Section `json:"sections"`
}

func initSectionsFile() error {
	if _, err := os.Stat(ClipseConfig.SectionsFilePath); !os.IsNotExist(err) {
		return err // nil when the file already exists
	}
	return writeSections(Sections{Sections: []Section{}})
}

// readSections returns the sections held on disk. A missing or unparseable file
// yields an empty set rather than an error: a corrupt file must not crash the
// TUI, and writeSections is not called, so the bad file is left intact for the
// user to recover rather than being silently overwritten.
func readSections() Sections {
	empty := Sections{Sections: []Section{}}

	raw, err := os.ReadFile(ClipseConfig.SectionsFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			utils.LogERROR(fmt.Sprintf("failed to read sections file: %s", err))
		}
		return empty
	}

	var data Sections
	if err := json.Unmarshal(raw, &data); err != nil {
		utils.LogERROR(fmt.Sprintf("failed to parse sections file, treating as empty: %s", err))
		return empty
	}

	if data.Sections == nil {
		data.Sections = []Section{}
	}
	return data
}

// writeSections persists atomically: write a temp file, then rename it over the
// target. The history file is disposable, so it writes in place; this is the
// permanent hand-curated store, and a crash mid-write must not destroy it.
func writeSections(data Sections) error {
	encoded, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal sections: %w", err)
	}

	if !utils.DiskspaceAvailable(len(encoded)) {
		return fmt.Errorf("not enough disk space to write sections file")
	}

	tmp := ClipseConfig.SectionsFilePath + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0644); err != nil {
		return fmt.Errorf("failed writing sections temp file: %w", err)
	}

	if err := os.Rename(tmp, ClipseConfig.SectionsFilePath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to replace sections file: %w", err)
	}
	return nil
}

func GetSections() []Section {
	return readSections().Sections
}

func GetSectionItems(name string) []SectionItem {
	for _, s := range readSections().Sections {
		if s.Name == name {
			return s.Items
		}
	}
	return []SectionItem{}
}

func AddSection(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("section name cannot be empty")
	}

	data := readSections()
	for _, s := range data.Sections {
		if s.Name == name {
			return fmt.Errorf("section %q already exists", name)
		}
	}

	data.Sections = append(data.Sections, Section{
		Name:    name,
		Created: utils.GetTime(),
		Items:   []SectionItem{},
	})

	return writeSections(data)
}

func DeleteSection(name string) error {
	data := readSections()

	kept := []Section{}
	for _, s := range data.Sections {
		if s.Name != name {
			kept = append(kept, s)
		}
	}
	data.Sections = kept

	return writeSections(data)
}

// AddSectionItem stores a snapshot of value in the named section.
func AddSectionItem(sectionName, value string) error {
	data := readSections()

	for i, s := range data.Sections {
		if s.Name != sectionName {
			continue
		}

		// newest first, matching the ordering of the clipboard history list
		data.Sections[i].Items = append(
			[]SectionItem{{
				Value: utils.SanitizeChars(value),
				Added: utils.GetTime(),
			}},
			data.Sections[i].Items...,
		)

		return writeSections(data)
	}

	return fmt.Errorf("section %q does not exist", sectionName)
}

// MaskSectionItem hides an item's value behind a label. One-way by design: a
// value saved as a secret should not be revealable from the list with a single
// keypress. The stored value is untouched, so copying still yields the real
// thing.
func MaskSectionItem(sectionName, timeStamp, label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("a masked item needs a label")
	}

	data := readSections()
	for i, s := range data.Sections {
		if s.Name != sectionName {
			continue
		}
		for j, item := range s.Items {
			if item.Added != timeStamp {
				continue
			}
			data.Sections[i].Items[j].Label = label
			data.Sections[i].Items[j].Masked = true
			return writeSections(data)
		}
		return fmt.Errorf("item not found in section %q", sectionName)
	}

	return fmt.Errorf("section %q does not exist", sectionName)
}

func DeleteSectionItems(sectionName string, timeStamps []string) error {
	toDelete := make(map[string]bool, len(timeStamps))
	for _, ts := range timeStamps {
		toDelete[ts] = true
	}

	data := readSections()
	for i, s := range data.Sections {
		if s.Name != sectionName {
			continue
		}

		kept := []SectionItem{}
		for _, item := range s.Items {
			if !toDelete[item.Added] {
				kept = append(kept, item)
			}
		}
		data.Sections[i].Items = kept

		return writeSections(data)
	}

	return fmt.Errorf("section %q does not exist", sectionName)
}
