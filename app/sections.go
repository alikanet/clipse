package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/savedra1/clipse/config"
	"github.com/savedra1/clipse/display"
	"github.com/savedra1/clipse/utils"
)

/* The sections screen: a user-curated store of named text snippets.

Self-contained. It owns its own lists, keymap and internal state, and shares no
fields with the root Model, which delegates to it wholesale while active. That
keeps this feature out of the root model's boolean view-flags and the
setPreviewKeys/setConfirmationKeys machinery, which do not scale to the six
states below.
*/

type sectionsState int

const (
	stateSectionList   sectionsState = iota // browsing sections
	stateItemList                           // browsing one section's items
	stateNameInput                          // typing a new section name
	stateValueInput                         // typing a new item value
	stateImport                             // picking history entries to import
	stateConfirmDelete                      // confirming a section deletion
	statePickTarget                         // choosing which section to add history items to
)

// what the confirmation screen is currently asking about
type confirmKind int

const (
	confirmSection confirmKind = iota
	confirmItem
)

type sectionsKeyMap struct {
	add          key.Binding
	remove       key.Binding
	copy         key.Binding
	importItem   key.Binding
	choose       key.Binding
	back         key.Binding
	selectSingle key.Binding
	togglePinned key.Binding
}

func newSectionsKeyMap(c map[string]string) *sectionsKeyMap {
	return &sectionsKeyMap{
		add: key.NewBinding(
			key.WithKeys(parseKeys(c["sectionAdd"])...),
			key.WithHelp(getHelpChar(c["sectionAdd"]), "add"),
		),
		remove: key.NewBinding(
			key.WithKeys(parseKeys(c["sectionDelete"])...),
			key.WithHelp(getHelpChar(c["sectionDelete"]), "delete"),
		),
		copy: key.NewBinding(
			key.WithKeys(parseKeys(c["sectionCopy"])...),
			key.WithHelp(getHelpChar(c["sectionCopy"]), "copy"),
		),
		importItem: key.NewBinding(
			key.WithKeys(parseKeys(c["sectionImport"])...),
			key.WithHelp(getHelpChar(c["sectionImport"]), "import from history"),
		),
		choose: key.NewBinding(
			key.WithKeys(parseKeys(c["choose"])...),
			key.WithHelp(getHelpChar(c["choose"]), "open"),
		),
		back: key.NewBinding(
			key.WithKeys(parseKeys(c["quit"])...),
			key.WithHelp(getHelpChar(c["quit"]), "back"),
		),
		selectSingle: key.NewBinding(
			key.WithKeys(parseKeys(c["selectSingle"])...),
			key.WithHelp(getHelpChar(c["selectSingle"]), "select"),
		),
		togglePinned: key.NewBinding(
			key.WithKeys(parseKeys(c["togglePinned"])...),
			key.WithHelp(getHelpChar(c["togglePinned"]), "pinned only"),
		),
	}
}

type sectionsModel struct {
	state        sectionsState
	list         list.Model // sections, or the items of the current section
	importList   list.Model // history picker
	confirmList  list.Model // delete-section confirmation
	input        textinput.Model
	keys         *sectionsKeyMap
	theme        config.CustomTheme
	current      string      // section currently open, when in stateItemList
	confirmKind  confirmKind // what the confirmation screen is asking about
	importPinned bool        // pinned-only toggle in the import picker
	exit         bool        // signals the root model to quit (copy-and-exit)

	// set when the screen was opened from the history list to file items into a
	// section, rather than entered to browse
	pickMode bool
	pending  []string // values waiting to be filed
	status   string   // message for the root model to show once it regains control

	width  int
	height int
}

func newSectionsModel(theme config.CustomTheme) sectionsModel {
	keyConfig := config.ClipseConfig.KeyBindings
	del := itemDelegate{theme: theme}

	l := list.New([]list.Item{}, del, 0, 0)
	l.KeyMap = defaultOverrides(keyConfig)
	l.Title = sectionsTitle
	l.SetShowHelp(false)
	l.Filter = sanitizedFilter
	l.Styles.PaginationStyle = style.MarginBottom(1).MarginLeft(2)

	imp := list.New([]list.Item{}, del, 0, 0)
	imp.KeyMap = defaultOverrides(keyConfig)
	imp.Title = sectionImportTitle
	imp.SetShowHelp(false)
	imp.Filter = sanitizedFilter
	imp.Styles.PaginationStyle = style.MarginBottom(1).MarginLeft(2)

	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 0 // unlimited: snippets can be long

	m := sectionsModel{
		state:       stateSectionList,
		list:        styledList(l, theme),
		importList:  styledList(imp, theme),
		confirmList: styledList(newConfirmationList(del), theme),
		input:       ti,
		keys:        newSectionsKeyMap(keyConfig),
		theme:       theme,
	}

	return m
}

/*
	DATA -> LIST ITEMS
*/

func sectionListItems() []list.Item {
	items := []list.Item{}
	for _, s := range config.GetSections() {
		desc := fmt.Sprintf("%d items", len(s.Items))
		if len(s.Items) == 1 {
			desc = "1 item"
		}
		items = append(items, item{
			title:           s.Name,
			titleBase:       s.Name,
			titleFull:       s.Name,
			description:     desc,
			descriptionBase: desc,
			filePath:        "null",
		})
	}
	return items
}

func sectionItemListItems(sectionName string) []list.Item {
	items := []list.Item{}
	for _, si := range config.GetSectionItems(sectionName) {
		shortened := utils.Shorten(si.Value, config.ClipseConfig.MaxEntryLength)
		desc := "Date added: " + si.Added
		items = append(items, item{
			title:           shortened,
			titleBase:       shortened,
			titleFull:       si.Value,
			description:     desc,
			descriptionBase: desc,
			timeStamp:       si.Added,
			filePath:        "null",
		})
	}
	return items
}

// pickTargetItems lists the sections to file into, with a row for creating one
// on the spot so an empty store is not a dead end.
func pickTargetItems() []list.Item {
	newRow := item{
		title:           newSectionRow,
		titleBase:       newSectionRow,
		description:     "create a section and add to it",
		descriptionBase: "create a section and add to it",
		filePath:        "null",
		isAction:        true,
	}
	return append([]list.Item{newRow}, sectionListItems()...)
}

// beginPick opens the screen in "file these values into a section" mode.
func (m sectionsModel) beginPick(values []string) sectionsModel {
	m.pickMode = true
	m.pending = values
	m.state = statePickTarget
	m.status = ""
	m = m.refresh()
	m.list.Select(0)
	return m
}

// filePending writes the pending values into the named section and reports what
// happened, for the root model to show on the history list.
func (m sectionsModel) filePending(sectionName string) sectionsModel {
	var failed int
	for _, v := range m.pending {
		if err := config.AddSectionItem(sectionName, v); err != nil {
			utils.LogERROR(fmt.Sprintf("failed to add item to section: %s", err))
			failed++
		}
	}

	added := len(m.pending) - failed
	switch {
	case failed > 0:
		m.status = fmt.Sprintf("Failed to add %d item(s) to %s", failed, sectionName)
	case added == 1:
		m.status = "Added to " + sectionName + ": " + utils.Shorten(m.pending[0], config.ClipseConfig.MaxEntryLength)
	default:
		m.status = fmt.Sprintf("Added %d items to %s", added, sectionName)
	}

	m.pending = nil
	m.pickMode = false
	return m
}

// refresh reloads the current view from disk. Called on entry and after writes.
func (m sectionsModel) refresh() sectionsModel {
	switch m.state {
	case statePickTarget:
		m.list.Title = pickTargetTitle
		m.list.SetItems(pickTargetItems())
	case stateItemList:
		m.list.Title = m.current
		m.list.SetItems(sectionItemListItems(m.current))
	default:
		m.list.Title = sectionsTitle
		m.list.SetItems(sectionListItems())
	}
	return m
}

func (m *sectionsModel) setSize(width, height int) {
	m.width, m.height = width, height
	h, v := appStyle.GetFrameSize()
	m.list.SetSize(width-h, height-v)
	m.importList.SetSize(width-h, height-v)
	m.confirmList.SetSize(width-h, height-v)
	m.input.Width = width - h - 4
}

/*
	UPDATE
*/

// Update returns the updated model, a command, and whether the root model
// should return to the clipboard history screen.
func (m sectionsModel) Update(msg tea.Msg) (sectionsModel, tea.Cmd, bool) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.setSize(msg.Width, msg.Height)
		return m, nil, false

	case tea.KeyMsg:
		switch m.state {
		case stateNameInput, stateValueInput:
			return m.updateInput(msg)
		case stateConfirmDelete:
			return m.updateConfirm(msg)
		case stateImport:
			return m.updateImport(msg)
		case statePickTarget:
			return m.updatePickTarget(msg)
		case stateItemList:
			return m.updateItemList(msg)
		default:
			return m.updateSectionList(msg)
		}
	}

	// non-key messages (status message timers, etc.) still need to reach the lists
	var cmd tea.Cmd
	switch m.state {
	case stateImport:
		m.importList, cmd = m.importList.Update(msg)
	case stateConfirmDelete:
		m.confirmList, cmd = m.confirmList.Update(msg)
	default:
		m.list, cmd = m.list.Update(msg)
	}
	return m, cmd, false
}

func (m sectionsModel) updateSectionList(msg tea.KeyMsg) (sectionsModel, tea.Cmd, bool) {
	// while filtering, letter keys must type into the filter, not fire actions
	if m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd, false
	}

	switch {
	case key.Matches(msg, m.keys.add):
		m.state = stateNameInput
		m.input.Reset()
		m.input.Placeholder = "e.g. Emails"
		m.input.Focus()
		return m, textinput.Blink, false

	case key.Matches(msg, m.keys.back):
		if m.list.IsFiltered() {
			m.list.ResetFilter()
			return m, nil, false
		}
		return m, nil, true // back to the history screen
	}

	i, ok := m.list.SelectedItem().(item)
	if !ok { // empty list: nothing below applies
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd, false
	}

	switch {
	case key.Matches(msg, m.keys.choose):
		m.current = i.titleFull
		m.state = stateItemList
		m.list.ResetFilter()
		m = m.refresh()
		m.list.Select(0)
		return m, nil, false

	case key.Matches(msg, m.keys.copy):
		values := []string{}
		for _, si := range config.GetSectionItems(i.titleFull) {
			values = append(values, si.Value)
		}
		if len(values) == 0 {
			return m, m.list.NewStatusMessage(statusMessageStyle("Section is empty")), false
		}
		display.DisplayServer.CopyText(strings.Join(values, "\n"))
		return m, m.list.NewStatusMessage(
			statusMessageStyle(fmt.Sprintf("Copied section: %s", i.titleFull)),
		), false

	case key.Matches(msg, m.keys.remove):
		// deleting a section takes its items with it, so confirm first
		m.state = stateConfirmDelete
		m.confirmKind = confirmSection
		m.confirmList.Title = fmt.Sprintf("Delete section %q and its items?", i.titleFull)
		m.confirmList.Select(0)
		return m, nil, false
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd, false
}

// updatePickTarget handles the "which section should these go in?" screen, shown
// when the user files items from the history list. Returning true hands control
// back to the history screen, which is where the user came from.
func (m sectionsModel) updatePickTarget(msg tea.KeyMsg) (sectionsModel, tea.Cmd, bool) {
	if m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd, false
	}

	switch {
	case key.Matches(msg, m.keys.back):
		if m.list.IsFiltered() {
			m.list.ResetFilter()
			return m, nil, false
		}
		m.pending = nil
		m.pickMode = false
		m.status = ""
		return m, nil, true // cancelled: straight back to the history list
	}

	i, ok := m.list.SelectedItem().(item)
	if !ok {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd, false
	}

	if key.Matches(msg, m.keys.choose) {
		if i.isAction { // "+ New section": name it, then file into it
			m.state = stateNameInput
			m.input.Reset()
			m.input.Placeholder = "e.g. Emails"
			m.input.Focus()
			return m, textinput.Blink, false
		}

		m = m.filePending(i.titleFull)
		return m, nil, true // done: back to the history list
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd, false
}

func (m sectionsModel) updateItemList(msg tea.KeyMsg) (sectionsModel, tea.Cmd, bool) {
	if m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd, false
	}

	switch {
	case key.Matches(msg, m.keys.add):
		m.state = stateValueInput
		m.input.Reset()
		m.input.Placeholder = "value to save"
		m.input.Focus()
		return m, textinput.Blink, false

	case key.Matches(msg, m.keys.importItem):
		m.state = stateImport
		m.importPinned = false
		m.importList.Title = sectionImportTitle
		m.importList.SetItems(filterItems(config.GetHistory(), false))
		m.importList.Select(0)
		return m, nil, false

	case key.Matches(msg, m.keys.back):
		if m.list.IsFiltered() {
			m.list.ResetFilter()
			return m, nil, false
		}
		m.state = stateSectionList
		m.current = ""
		m = m.refresh()
		return m, nil, false
	}

	i, ok := m.list.SelectedItem().(item)
	if !ok {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd, false
	}

	switch {
	case key.Matches(msg, m.keys.choose):
		// copy and exit, matching `choose` in the history list, so the existing
		// auto-paste path still fires
		display.DisplayServer.CopyText(i.titleFull)
		if KeepEnabled {
			return m, m.list.NewStatusMessage(
				statusMessageStyle("Copied to clipboard: " + i.title),
			), false
		}
		m.exit = true
		return m, nil, false

	case key.Matches(msg, m.keys.copy):
		// copy but stay, so several items can be grabbed without relaunching
		display.DisplayServer.CopyText(i.titleFull)
		return m, m.list.NewStatusMessage(
			statusMessageStyle("Copied to clipboard: " + i.title),
		), false

	case key.Matches(msg, m.keys.remove):
		// a saved item was put here deliberately, so confirm before removing it
		m.state = stateConfirmDelete
		m.confirmKind = confirmItem
		m.confirmList.Title = fmt.Sprintf("Delete %q?", i.title)
		m.confirmList.Select(0)
		return m, nil, false
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd, false
}

func (m sectionsModel) updateImport(msg tea.KeyMsg) (sectionsModel, tea.Cmd, bool) {
	if m.importList.SettingFilter() {
		var cmd tea.Cmd
		m.importList, cmd = m.importList.Update(msg)
		return m, cmd, false
	}

	switch {
	case key.Matches(msg, m.keys.back):
		if m.importList.IsFiltered() {
			m.importList.ResetFilter()
			return m, nil, false
		}
		m.state = stateItemList
		m = m.refresh()
		return m, nil, false

	case key.Matches(msg, m.keys.togglePinned):
		// the full history is shown by default; this narrows it to pinned only,
		// mirroring the same toggle in the history list
		m.importPinned = !m.importPinned
		items := filterItems(config.GetHistory(), m.importPinned)
		if len(items) == 0 && m.importPinned {
			m.importPinned = false
			return m, m.importList.NewStatusMessage(statusMessageStyle("No pinned items")), false
		}
		m.importList.Title = sectionImportTitle
		if m.importPinned {
			m.importList.Title = "Pinned " + sectionImportTitle
		}
		m.importList.SetItems(items)
		m.importList.Select(0)
		return m, nil, false
	}

	i, ok := m.importList.SelectedItem().(item)
	if !ok {
		var cmd tea.Cmd
		m.importList, cmd = m.importList.Update(msg)
		return m, cmd, false
	}

	switch {
	case key.Matches(msg, m.keys.selectSingle):
		if m.importList.IsFiltered() {
			return m, m.importList.NewStatusMessage(
				statusMessageStyle("cannot select items with filter applied"),
			), false
		}
		index := m.importList.Index()
		i.selected = !i.selected
		m.importList.SetItem(index, i)
		return m, nil, false

	case key.Matches(msg, m.keys.choose):
		values := m.importSelection(i)

		var failed int
		for _, v := range values {
			// snapshot: the value is copied, with no link back to the history
			// entry, which the history will eventually evict and delete
			if err := config.AddSectionItem(m.current, v); err != nil {
				utils.LogERROR(fmt.Sprintf("failed to import item into section: %s", err))
				failed++
			}
		}

		m.state = stateItemList
		m = m.refresh()

		if failed > 0 {
			return m, m.list.NewStatusMessage(
				statusMessageStyle(fmt.Sprintf("Failed to import %d item(s)", failed)),
			), false
		}

		statusMsg := "Imported: " + utils.Shorten(values[0], config.ClipseConfig.MaxEntryLength)
		if len(values) > 1 {
			statusMsg = fmt.Sprintf("Imported %d items", len(values))
		}
		return m, m.list.NewStatusMessage(statusMessageStyle(statusMsg)), false
	}

	var cmd tea.Cmd
	m.importList, cmd = m.importList.Update(msg)
	return m, cmd, false
}

// importSelection returns the values to import: every multi-selected entry, or
// the item under the cursor when nothing is explicitly selected. Images are
// skipped -- sections are text only.
func (m sectionsModel) importSelection(cursor item) []string {
	values := []string{}

	for _, li := range m.importList.Items() {
		it, ok := li.(item)
		if !ok || !it.selected {
			continue
		}
		if it.filePath != "null" {
			continue // image entry
		}
		values = append(values, it.titleFull)
	}

	if len(values) == 0 && cursor.filePath == "null" {
		values = append(values, cursor.titleFull)
	}

	return values
}

func (m sectionsModel) updateConfirm(msg tea.KeyMsg) (sectionsModel, tea.Cmd, bool) {
	// the screen behind the confirmation, returned to either way
	previous := stateSectionList
	if m.confirmKind == confirmItem {
		previous = stateItemList
	}

	switch {
	case key.Matches(msg, m.keys.back):
		m.state = previous
		return m, nil, false

	case key.Matches(msg, m.keys.choose):
		confirmed := m.confirmList.Index() == 1 // 0 = No, 1 = Yes
		m.state = previous

		if !confirmed {
			return m, nil, false
		}

		// the list behind the confirmation is untouched, so the highlighted row
		// is still the one the user pressed delete on
		i, ok := m.list.SelectedItem().(item)
		if !ok {
			return m, nil, false
		}

		if m.confirmKind == confirmItem {
			if err := config.DeleteSectionItems(m.current, []string{i.timeStamp}); err != nil {
				return m, m.list.NewStatusMessage(statusMessageStyle(err.Error())), false
			}
			m = m.refresh()
			return m, m.list.NewStatusMessage(
				statusMessageStyle("Deleted: " + i.title),
			), false
		}

		if err := config.DeleteSection(i.titleFull); err != nil {
			return m, m.list.NewStatusMessage(statusMessageStyle(err.Error())), false
		}

		m = m.refresh()
		return m, m.list.NewStatusMessage(
			statusMessageStyle("Deleted section: " + i.titleFull),
		), false
	}

	var cmd tea.Cmd
	m.confirmList, cmd = m.confirmList.Update(msg)
	return m, cmd, false
}

func (m sectionsModel) updateInput(msg tea.KeyMsg) (sectionsModel, tea.Cmd, bool) {
	switch msg.Type {

	case tea.KeyEsc:
		m.input.Blur()
		switch {
		case m.pickMode:
			// cancelling the name prompt returns to the section choice, not to
			// the history list: the pending items are still waiting to be filed
			m.state = statePickTarget
		case m.state == stateNameInput:
			m.state = stateSectionList
		default:
			m.state = stateItemList
		}
		m = m.refresh()
		return m, nil, false

	case tea.KeyEnter:
		value := m.input.Value()
		m.input.Blur()

		if m.state == stateNameInput {
			if err := config.AddSection(value); err != nil {
				if m.pickMode { // let the user correct a bad or duplicate name
					m.state = statePickTarget
					m = m.refresh()
					return m, m.list.NewStatusMessage(statusMessageStyle(err.Error())), false
				}
				m.state = stateSectionList
				m = m.refresh()
				return m, m.list.NewStatusMessage(statusMessageStyle(err.Error())), false
			}

			name := strings.TrimSpace(value)

			// created from the history list: file the pending items and go back
			if m.pickMode {
				m = m.filePending(name)
				m.state = stateSectionList
				m = m.refresh()
				return m, nil, true
			}

			// drop straight into the new section
			m.current = name
			m.state = stateItemList
			m = m.refresh()
			return m, m.list.NewStatusMessage(
				statusMessageStyle("Created section: " + m.current),
			), false
		}

		if strings.TrimSpace(value) == "" {
			m.state = stateItemList
			m = m.refresh()
			return m, m.list.NewStatusMessage(statusMessageStyle("Nothing to add")), false
		}

		if err := config.AddSectionItem(m.current, value); err != nil {
			m.state = stateItemList
			m = m.refresh()
			return m, m.list.NewStatusMessage(statusMessageStyle(err.Error())), false
		}

		m.state = stateItemList
		m = m.refresh()
		m.list.Select(0) // new items land at the top
		return m, m.list.NewStatusMessage(
			statusMessageStyle("Added: " + utils.Shorten(value, config.ClipseConfig.MaxEntryLength)),
		), false
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd, false
}

/*
	VIEW
*/

func (m sectionsModel) View() string {
	render := style.PaddingLeft(1).Render
	helpView := func(bindings []key.Binding) string {
		return style.PaddingLeft(2).Render(m.list.Help.ShortHelpView(bindings))
	}

	switch m.state {

	case stateNameInput, stateValueInput:
		prompt := sectionNamePrompt
		if m.state == stateValueInput {
			prompt = sectionValuePrompt
		}
		return render(fmt.Sprintf(
			"\n  %s\n\n  %s\n\n%s",
			style.Foreground(lipgloss.Color(m.theme.TitleFore)).Render(prompt),
			m.input.View(),
			helpView([]key.Binding{
				key.NewBinding(key.WithKeys("enter"), key.WithHelp(enterChar, "save")),
				key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
			}),
		))

	case stateConfirmDelete:
		// the `choose` binding reads "open" everywhere else, which is meaningless
		// on a yes/no prompt
		confirmChoose := key.NewBinding(
			key.WithKeys(m.keys.choose.Keys()...),
			key.WithHelp(getHelpChar(config.ClipseConfig.KeyBindings["choose"]), "confirm"),
		)
		return render(m.confirmList.View() + "\n" + helpView([]key.Binding{
			confirmChoose, m.keys.back,
		}))

	case stateImport:
		if m.importList.SettingFilter() {
			return render(m.importList.View())
		}
		return render(m.importList.View() + "\n" + helpView([]key.Binding{
			m.keys.choose, m.keys.selectSingle, m.keys.togglePinned, m.keys.back,
		}))

	case statePickTarget:
		if m.list.SettingFilter() {
			return render(m.list.View())
		}
		pick := key.NewBinding(
			key.WithKeys(m.keys.choose.Keys()...),
			key.WithHelp(getHelpChar(config.ClipseConfig.KeyBindings["choose"]), "add to section"),
		)
		cancel := key.NewBinding(
			key.WithKeys(m.keys.back.Keys()...),
			key.WithHelp(getHelpChar(config.ClipseConfig.KeyBindings["quit"]), "cancel"),
		)
		return render(m.list.View() + "\n" + helpView([]key.Binding{pick, cancel}))

	case stateItemList:
		if m.list.SettingFilter() {
			return render(m.list.View())
		}
		return render(m.list.View() + "\n" + helpView([]key.Binding{
			m.keys.choose, m.keys.copy, m.keys.add, m.keys.importItem, m.keys.remove, m.keys.back,
		}))

	default:
		if m.list.SettingFilter() {
			return render(m.list.View())
		}
		return render(m.list.View() + "\n" + helpView([]key.Binding{
			m.keys.choose, m.keys.add, m.keys.copy, m.keys.remove, m.keys.back,
		}))
	}
}
