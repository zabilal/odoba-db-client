package theme

import (
	"image/color"
	"reflect"
)

// Reflective access to palette roles.
//
// Used by the theme gallery to enumerate every role without a hand-maintained
// list that drifts, and by the contrast suite to prove that no role was added
// without being checked.

// RoleNames returns every Palette field name, in declaration order.
func RoleNames() []string {
	t := reflect.TypeOf(Palette{})
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		out = append(out, t.Field(i).Name)
	}
	return out
}

// RoleColor returns a palette role by name.
func RoleColor(p Palette, name string) (color.NRGBA, bool) {
	v := reflect.ValueOf(p).FieldByName(name)
	if !v.IsValid() {
		return color.NRGBA{}, false
	}
	c, ok := v.Interface().(color.NRGBA)
	return c, ok
}
