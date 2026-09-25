package shell

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/app/connstr"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
)

// testTimeout bounds "Test Connection". It is generous: it exists so a
// black-holed host cannot leave the form waiting forever, not to judge a slow
// network.
const testTimeout = 20 * time.Second

// tlsModes are the encryption choices, strongest first, in words rather than
// libpq's mode names. An empty mode is the driver default, which verifies
// (source.TLSConfig), so it shows as the first choice.
var tlsModes = []struct{ mode, label string }{
	{"verify-full", "Verify certificate and host name"},
	{"verify-ca", "Verify certificate only"},
	{"require", "Encrypt without verifying"},
	{"disable", "Don't encrypt"},
}

// environments are the environment choices (FR-1.10), in words.
var environments = []struct{ value, label string }{
	{"", "None"},
	{string(source.EnvLocal), "Local"},
	{string(source.EnvDev), "Development"},
	{string(source.EnvStaging), "Staging"},
	{string(source.EnvProduction), "Production"},
}

// formError is a message for the user, worded as a sentence.
type formError string

func (e formError) Error() string { return string(e) }

// connForm is the New / Edit Connection sheet (FR-1.2–FR-1.4, T1.24).
//
// Its driver fields come from the driver's Descriptor, so each source shows
// only what it needs. Saving goes through app.Connections, which sends secrets
// to the keychain; the form never writes settings or secrets itself.
type connForm struct {
	s       *Shell
	id      string                // "" when creating
	base    store.SavedConnection // what the form does not edit survives from here
	drivers []source.Descriptor
	desc    source.Descriptor

	form   *widget.Form
	name   *widget.Entry
	driver *widget.Select
	url    *widget.Entry
	inputs map[string]*input
	tls    *widget.Select
	env    *widget.Select
	// folder is the folder the connection is in, and folderIDs its choices'
	// folders, the first none.
	folder    *widget.Select
	folderIDs []string
	readOnly  *widget.Check
	// aiSchema and aiData are this connection's opt-in to the assistant
	// (FR-14.4). Two switches rather than one, because being willing to ask
	// about a schema is not being willing to send the rows.
	aiSchema *widget.Check
	aiData   *widget.Check
	result   *widget.Label

	testBtn, cancelBtn, saveBtn *widget.Button

	dlg      *dialog.CustomDialog
	stopTest context.CancelFunc
}

func (s *Shell) showConnectionForm(id string) *connForm {
	f, err := newConnForm(s, id)
	if err != nil {
		s.showError(err)
		return nil
	}
	f.show()
	return f
}

func newConnForm(s *Shell, id string) (*connForm, error) {
	f := &connForm{s: s, id: id, drivers: source.Drivers()}
	if len(f.drivers) == 0 {
		return nil, errors.New("this build includes no database drivers")
	}
	f.desc = f.drivers[0]
	if id != "" {
		c, ok := s.d.Conns.Get(id)
		if !ok {
			return nil, errors.New("the connection no longer exists")
		}
		d, ok := driverByID(f.drivers, c.Driver)
		if !ok {
			return nil, fmt.Errorf("this build does not include the %q driver", c.Driver)
		}
		f.base, f.desc = c, d
	}
	if id == "" { // a new connection goes in the folder selected, if one is
		f.base.Folder, _ = s.selectedFolder()
	}
	f.build()
	f.fill(f.base, nil)
	return f, nil
}

func (f *connForm) build() {
	f.name = widget.NewEntry()
	f.name.SetPlaceHolder("Defaults to the host and database")
	f.name.OnSubmitted = func(string) { f.save() }

	names := make([]string, len(f.drivers))
	for i, d := range f.drivers {
		names[i] = d.Name
	}
	f.driver = widget.NewSelect(names, nil)
	f.driver.SetSelected(f.desc.Name)
	f.driver.OnChanged = func(name string) {
		if d, ok := driverByName(f.drivers, name); ok && d.ID != f.desc.ID {
			f.setDriver(d)
		}
	}
	if f.id != "" {
		// Another engine would be a different connection, not an edit.
		f.driver.Disable()
	}

	f.url = widget.NewEntry()
	f.url.SetPlaceHolder("Paste a connection URL to fill in the fields")
	f.url.OnSubmitted = f.applyURL

	tls := make([]string, len(tlsModes))
	for i, m := range tlsModes {
		tls[i] = m.label
	}
	f.tls = widget.NewSelect(tls, nil)
	env := make([]string, len(environments))
	for i, e := range environments {
		env[i] = e.label
	}
	f.env = widget.NewSelect(env, nil)
	folders := []string{"None"}
	f.folderIDs = []string{""}
	for _, fo := range f.s.d.Conns.Folders() {
		folders = append(folders, fo.Name)
		f.folderIDs = append(f.folderIDs, fo.ID)
	}
	f.folder = widget.NewSelect(folders, nil)
	f.readOnly = widget.NewCheck("Read-only: refuse statements that change data", nil)
	f.aiData = widget.NewCheck("…and its rows, not only its schema", nil)
	f.aiSchema = widget.NewCheck("The assistant may be asked about this connection", func(on bool) {
		// Sending rows is meaningless without the first switch, so it follows
		// it: a connection nobody opted in has not opted in to anything.
		if on {
			f.aiData.Enable()
			return
		}
		f.aiData.SetChecked(false)
		f.aiData.Disable()
	})
	f.aiData.Disable()
	f.result = widget.NewLabel("")
	f.result.Wrapping = fyne.TextWrapWord

	f.form = &widget.Form{}
	f.setDriver(f.desc)

	f.testBtn = widget.NewButton("Test Connection", f.test)
	f.cancelBtn = widget.NewButton("Cancel", f.close)
	f.saveBtn = widget.NewButton("Save", f.save)
	f.saveBtn.Importance = widget.HighImportance
}

// setDriver rebuilds the driver fields, carrying over values for fields the
// two drivers share, so choosing another engine does not wipe the host.
func (f *connForm) setDriver(d source.Descriptor) {
	old := f.inputs
	f.desc = d
	f.inputs = make(map[string]*input, len(d.Fields))
	for _, fd := range d.Fields {
		in := newInput(fd, f.save)
		if fd.Kind == source.FieldFile {
			in.choose = widget.NewButton("Choose…", func() { f.chooseFile(in) })
			in.obj = container.NewBorder(nil, nil, nil, in.choose, in.entry)
		}
		if o, ok := old[fd.Key]; ok && o.f.Kind == fd.Kind {
			in.set(o.value())
		}
		f.inputs[fd.Key] = in
	}
	f.layout()
}

func (f *connForm) layout() {
	fill := widget.NewButton("Fill In", func() { f.applyURL(f.url.Text) })
	items := []*widget.FormItem{
		widget.NewFormItem("Name", f.name),
		widget.NewFormItem("Type", f.driver),
		widget.NewFormItem("URL", container.NewBorder(nil, nil, nil, fill, f.url)),
	}
	for _, fd := range f.desc.Fields {
		it := widget.NewFormItem(fd.Label, f.inputs[fd.Key].obj)
		it.HintText = f.hint(fd)
		items = append(items, it)
	}
	// Encryption in transit means nothing to a database that is a file.
	if f.networked() {
		items = append(items, widget.NewFormItem("Encryption", f.tls))
	}
	if len(f.folderIDs) > 1 { // with no folders, there is nothing to choose
		items = append(items, widget.NewFormItem("Folder", f.folder))
	}
	items = append(items,
		widget.NewFormItem("Environment", f.env),
		widget.NewFormItem("", f.readOnly),
		widget.NewFormItem("Assistant", f.aiSchema),
		widget.NewFormItem("", f.aiData),
		widget.NewFormItem("", f.result),
	)
	f.form.Items = items
	f.form.Refresh()
}

// assistantOptIn is what this connection has agreed the assistant may see, or
// nil where it has agreed nothing — which is what every connection is until
// somebody ticks the box, and is how "off by default" is written in a file
// (FR-14.4).
func (f *connForm) assistantOptIn() *store.ConnectionAssistant {
	if !f.aiSchema.Checked {
		return nil
	}
	return &store.ConnectionAssistant{Enabled: true, Data: f.aiData.Checked}
}

// networked reports whether the driver connects over a network, which is
// what the encryption settings apply to.
func (f *connForm) networked() bool {
	for _, fd := range f.desc.Fields {
		if fd.Key == "host" {
			return true
		}
	}
	return false
}

func (f *connForm) hint(fd source.Field) string {
	if !fd.Secret {
		return fd.Help
	}
	switch {
	case !f.s.d.Conns.Vault().Persistent():
		return "No keychain is available, so this is kept only until you quit."
	case slices.Contains(f.base.Secrets, fd.Key):
		return "Saved in the keychain. Leave blank to keep it."
	}
	return "Stored in the keychain, never in the settings file."
}

// fill shows a connection in the form. typed holds secrets to show, which is
// only ever ones pasted in a URL: saved secrets are not read back out.
func (f *connForm) fill(c store.SavedConnection, typed map[string]string) {
	f.name.SetText(c.Name)
	for _, fd := range f.desc.Fields {
		in := f.inputs[fd.Key]
		switch {
		case fd.Secret:
			in.set(typed[fd.Key])
		case fd.Key == "host":
			in.set(c.Host)
		case fd.Key == "port":
			if c.Port != 0 {
				in.set(strconv.Itoa(c.Port))
			} else {
				in.set("")
			}
		case fd.Key == "database":
			in.set(c.Database)
		case fd.Key == "user":
			in.set(c.User)
		default:
			in.set(c.Params[fd.Key])
		}
	}
	f.tls.SetSelectedIndex(tlsIndex(c.TLS.Mode))
	f.env.SetSelectedIndex(envIndex(c.Environment))
	f.readOnly.SetChecked(c.ReadOnly)
	if a := c.Assistant; a != nil {
		f.aiSchema.SetChecked(a.Enabled)
		f.aiData.SetChecked(a.Enabled && a.Data)
	}
	f.folder.SetSelectedIndex(max(0, slices.Index(f.folderIDs, c.Folder)))
}

// collect turns the form into a connection and the secrets typed into it.
// Blank fields take the driver's defaults, which the placeholders show.
func (f *connForm) collect() (store.SavedConnection, map[string]string, error) {
	c := f.base
	c.Driver = f.desc.ID
	c.Name = strings.TrimSpace(f.name.Text)
	c.Host, c.Port, c.Database, c.User = "", 0, "", ""
	c.Params = maps.Clone(f.base.Params)
	typed := map[string]string{}
	for _, fd := range f.desc.Fields {
		v := f.inputs[fd.Key].value()
		if fd.Kind != source.FieldPassword && fd.Kind != source.FieldTextArea {
			v = strings.TrimSpace(v)
		}
		if v == "" && !fd.Secret {
			v = fd.Default
		}
		if v == "" && fd.Required && !(fd.Secret && slices.Contains(f.base.Secrets, fd.Key)) {
			return c, nil, formError(fd.Label + " is required.")
		}
		switch {
		case fd.Secret:
			if v != "" {
				typed[fd.Key] = v
			}
		case fd.Key == "host":
			c.Host = v
		case fd.Key == "port":
			if v != "" {
				p, err := strconv.Atoi(v)
				if err != nil || p < 1 || p > 65535 {
					return c, nil, formError(fd.Label + " must be a number from 1 to 65535.")
				}
				c.Port = p
			}
		case fd.Key == "database":
			c.Database = v
		case fd.Key == "user":
			c.User = v
		case v == "":
			delete(c.Params, fd.Key)
		default:
			if c.Params == nil {
				c.Params = map[string]string{}
			}
			c.Params[fd.Key] = v
		}
	}
	if c.Name == "" {
		c.Name = defaultName(c, f.desc)
	}
	if i := f.tls.SelectedIndex(); i >= 0 && f.networked() {
		c.TLS.Mode = tlsModes[i].mode
	}
	if i := f.env.SelectedIndex(); i >= 0 {
		c.Environment = environments[i].value
	}
	c.ReadOnly = f.readOnly.Checked
	c.Assistant = f.assistantOptIn()
	if i := f.folder.SelectedIndex(); i >= 0 {
		c.Folder = f.folderIDs[i]
	}
	return c, typed, nil
}

// applyURL fills the form from a pasted connection string (FR-1.3). The URL
// field is cleared afterwards: it may hold a password, which belongs in the
// password field, masked.
func (f *connForm) applyURL(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	res, err := connstr.Parse(text)
	if err != nil {
		f.say(err.Error(), true) // connstr errors never quote the input
		return
	}
	d, ok := driverByID(f.drivers, res.Conn.Driver)
	if !ok {
		f.say(fmt.Sprintf("This build cannot connect to %s yet.", res.Conn.Driver), true)
		return
	}
	if f.id != "" && d.ID != f.desc.ID {
		f.say("That URL is for a different kind of database. Create a new connection for it.", true)
		return
	}
	// Keep what a URL cannot say.
	c := res.Conn
	c.ID, c.Folder, c.Color, c.Secrets = f.base.ID, f.base.Folder, f.base.Color, f.base.Secrets
	if name := strings.TrimSpace(f.name.Text); name != "" {
		c.Name = name
	}
	if i := f.env.SelectedIndex(); i >= 0 {
		c.Environment = environments[i].value
	}
	c.ReadOnly = f.readOnly.Checked
	c.Assistant = f.assistantOptIn()
	f.base = c
	f.driver.SetSelected(d.Name)
	f.setDriver(d)
	f.fill(c, res.Secrets)
	f.url.SetText("")

	msg := "Filled in from the URL."
	if len(res.Warnings) > 0 {
		msg += " " + strings.Join(res.Warnings, " ")
	}
	f.say(msg, false)
}

func (f *connForm) test() {
	c, typed, err := f.collect()
	if err != nil {
		f.say(err.Error(), true)
		return
	}
	if f.stopTest != nil {
		f.stopTest()
	}
	ctx, cancel := context.WithTimeout(f.s.ctx, testTimeout)
	f.stopTest = cancel
	f.say("Connecting…", false)
	f.testBtn.Disable()
	go func() {
		r := f.s.d.Conns.Test(ctx, c, typed)
		f.s.d.Run(func() {
			f.testBtn.Enable()
			if errors.Is(ctx.Err(), context.Canceled) {
				return // the form closed, or another test replaced this one
			}
			cancel()
			f.say(testMessage(r), !r.OK)
		})
	}()
}

func testMessage(r app.TestResult) string {
	if r.OK {
		ms := r.Elapsed.Milliseconds()
		if product := strings.TrimSpace(r.Server.Product + " " + r.Server.Version); product != "" {
			return fmt.Sprintf("Connected to %s in %d ms.", product, ms)
		}
		return fmt.Sprintf("Connected in %d ms.", ms)
	}
	var parts []string
	for _, p := range []string{r.Hint, r.Detail} {
		if p != "" && !slices.Contains(parts, p) {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "The connection failed."
	}
	return strings.Join(parts, "\n")
}

func (f *connForm) save() {
	c, typed, err := f.collect()
	if err != nil {
		f.say(err.Error(), true)
		return
	}
	conns := f.s.d.Conns
	if f.id == "" {
		created, err := conns.Create(c, typed)
		if err != nil {
			f.say(err.Error(), true)
			return
		}
		c = created
	} else if err := conns.Update(c, app.SecretEdit{Set: typed}); err != nil {
		f.say(err.Error(), true)
		return
	}
	f.close()
	f.s.connectionSaved(c.ID, f.id != "")
}

func (f *connForm) show() {
	title := "New Connection"
	if f.id != "" {
		title = "Edit Connection"
	}
	f.dlg = dialog.NewCustomWithoutButtons(title, f.form, f.s.win)
	f.dlg.SetButtons([]fyne.CanvasObject{f.testBtn, f.cancelBtn, f.saveBtn})
	f.dlg.Show()
	f.resize()
	f.s.win.Canvas().Focus(f.name)
}

func (f *connForm) resize() {
	if f.dlg != nil {
		f.dlg.Resize(fyne.NewSize(560, f.dlg.MinSize().Height))
	}
}

func (f *connForm) close() {
	if f.stopTest != nil {
		f.stopTest()
	}
	if f.dlg != nil {
		f.dlg.Hide()
	}
}

// chooseFile fills a file field from the platform's open dialog (FR-15.5),
// starting in the folder of the file it names already.
func (f *connForm) chooseFile(in *input) {
	o := filedlg.Options{Message: "Choose the " + f.desc.Name + " database file", Accept: "Choose"}
	if cur := strings.TrimSpace(in.entry.Text); filepath.IsAbs(cur) {
		o.Directory = filepath.Dir(cur)
	}
	f.s.d.Files.Open(f.s.win, o, func(path string, err error) {
		switch {
		case err != nil:
			f.say(err.Error(), true)
		case path != "":
			in.entry.SetText(path)
		}
	})
}

func (f *connForm) say(msg string, bad bool) {
	f.result.Importance = widget.MediumImportance
	if bad {
		f.result.Importance = widget.DangerImportance
	}
	f.result.SetText(msg)
	f.resize() // a wrapped message can add lines
}

// input is one driver field's widget.
type input struct {
	f      source.Field
	obj    fyne.CanvasObject
	entry  *widget.Entry
	check  *widget.Check
	sel    *widget.Select
	choose *widget.Button // a file field's Choose…
}

func newInput(fd source.Field, submit func()) *input {
	in := &input{f: fd}
	switch fd.Kind {
	case source.FieldBool:
		in.check = widget.NewCheck("", nil)
		in.obj = in.check
	case source.FieldSelect:
		in.sel = widget.NewSelect(fd.Options, nil)
		in.obj = in.sel
	default:
		switch fd.Kind {
		case source.FieldPassword:
			in.entry = widget.NewPasswordEntry()
		case source.FieldTextArea:
			in.entry = widget.NewMultiLineEntry()
		default:
			in.entry = widget.NewEntry()
		}
		if fd.Kind != source.FieldTextArea {
			in.entry.OnSubmitted = func(string) { submit() }
		}
		in.entry.SetPlaceHolder(fd.Default)
		in.obj = in.entry
	}
	in.set("")
	return in
}

func (in *input) value() string {
	switch {
	case in.check != nil:
		return strconv.FormatBool(in.check.Checked)
	case in.sel != nil:
		return in.sel.Selected
	}
	return in.entry.Text
}

// set shows v. Checks and selects have no placeholder to show a default in,
// so a blank value selects the default instead.
func (in *input) set(v string) {
	switch {
	case in.check != nil:
		if v == "" {
			v = in.f.Default
		}
		in.check.SetChecked(v == "true")
	case in.sel != nil:
		if v == "" {
			v = in.f.Default
		}
		in.sel.SetSelected(v)
	default:
		in.entry.SetText(v)
	}
}

func driverByID(ds []source.Descriptor, id string) (source.Descriptor, bool) {
	for _, d := range ds {
		if d.ID == id {
			return d, true
		}
	}
	return source.Descriptor{}, false
}

func driverByName(ds []source.Descriptor, name string) (source.Descriptor, bool) {
	for _, d := range ds {
		if d.Name == name {
			return d, true
		}
	}
	return source.Descriptor{}, false
}

func tlsIndex(mode string) int {
	for i, m := range tlsModes {
		if m.mode == mode {
			return i
		}
	}
	return 0 // "" is the driver default, which verifies
}

func envIndex(v string) int {
	for i, e := range environments {
		if e.value == v {
			return i
		}
	}
	return 0
}

// defaultName names a connection the user did not name: its host and
// database, which is what they would most likely have typed.
func defaultName(c store.SavedConnection, d source.Descriptor) string {
	if c.Host == "" {
		return d.Name
	}
	if c.Database != "" {
		return c.Host + "/" + c.Database
	}
	return c.Host
}
