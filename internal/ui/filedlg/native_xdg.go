//go:build (linux && !android) || freebsd || openbsd || netbsd

package filedlg

import (
	"errors"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"github.com/godbus/dbus/v5"
)

const (
	portalName        = "org.freedesktop.portal.Desktop"
	portalPath        = "/org/freedesktop/portal/desktop"
	portalFileChooser = "org.freedesktop.portal.FileChooser"
	portalRequest     = "org.freedesktop.portal.Request"
)

// errNoPortal is a desktop with no file chooser portal to ask.
var errNoPortal = errors.New("no file chooser portal")

// show asks the desktop's file chooser portal, which GNOME, KDE and the
// others answer with their own dialog. Where no portal answers, Fyne's
// dialog stands in.
func show(w fyne.Window, o Options, save bool, done Done) {
	parent := "" // Wayland needs a handle exported for it, which Fyne has not
	if nw, ok := w.(driver.NativeWindow); ok {
		nw.RunNative(func(ctx any) {
			if c, ok := ctx.(driver.X11WindowContext); ok && c.WindowHandle != 0 {
				parent = fmt.Sprintf("x11:%x", c.WindowHandle)
			}
		})
	}
	go func() {
		path, err := askPortal(parent, o, save)
		fyne.Do(func() {
			if errors.Is(err, errNoPortal) {
				fallback(w, o, save, done)
				return
			}
			done(path, err)
		})
	}()
}

// askPortal calls the portal's SaveFile or OpenFile and waits for its
// Response. It is called directly, not through a library that unescapes
// the URI as a query and so turns a + in a file name into a space.
func askPortal(parent string, o Options, save bool) (string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return "", fmt.Errorf("%w: %v", errNoPortal, err)
	}
	defer conn.Close()
	if err := conn.AddMatchSignal(dbus.WithMatchInterface(portalRequest), dbus.WithMatchMember("Response")); err != nil {
		return "", fmt.Errorf("%w: %v", errNoPortal, err)
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	method, title := portalFileChooser+".OpenFile", "Open"
	if save {
		method, title = portalFileChooser+".SaveFile", "Save"
	}
	if o.Message != "" {
		title = o.Message
	}
	var handle dbus.ObjectPath
	if err := conn.Object(portalName, portalPath).Call(method, 0, parent, title, portalOptions(o, save)).Store(&handle); err != nil {
		return "", fmt.Errorf("%w: %v", errNoPortal, err)
	}
	for sig := range signals {
		if sig.Path != handle || sig.Name != portalRequest+".Response" || len(sig.Body) < 2 {
			continue
		}
		if code, _ := sig.Body[0].(uint32); code != 0 {
			return "", nil // cancelled, or ended some other way
		}
		results, _ := sig.Body[1].(map[string]dbus.Variant)
		var uris []string
		if v, ok := results["uris"]; ok {
			_ = v.Store(&uris)
		}
		if len(uris) == 0 {
			return "", nil
		}
		return pathOf(uris[0])
	}
	return "", errors.New("the file chooser closed without an answer")
}

// portalFilter is the portal's (sa(us)): a name and its glob rules.
type portalFilter struct {
	Name  string
	Rules []portalRule
}

type portalRule struct {
	Type    uint32 // 0 is a glob
	Pattern string
}

func portalOptions(o Options, save bool) map[string]dbus.Variant {
	opts := map[string]dbus.Variant{"modal": dbus.MakeVariant(true)}
	if o.Accept != "" {
		opts["accept_label"] = dbus.MakeVariant(o.Accept)
	}
	if save && o.Name != "" {
		opts["current_name"] = dbus.MakeVariant(o.Name)
	}
	if o.Directory != "" {
		opts["current_folder"] = dbus.MakeVariant(append([]byte(o.Directory), 0))
	}
	if fs := o.filters(); len(fs) > 0 {
		pf := make([]portalFilter, len(fs))
		for i, f := range fs {
			pf[i].Name = f.name
			for _, p := range f.patterns {
				pf[i].Rules = append(pf[i].Rules, portalRule{0, p})
			}
		}
		opts["filters"] = dbus.MakeVariant(pf)
		opts["current_filter"] = dbus.MakeVariant(pf[0])
	}
	return opts
}
