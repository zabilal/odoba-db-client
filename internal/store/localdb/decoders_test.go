package localdb

import "testing"

func TestADecoderIsRememberedByConnectionFieldAndTopic(t *testing.T) {
	d, _ := open(t)
	if _, ok, err := d.Decoder(ctx, "c1", "value", "orders"); ok || err != nil {
		t.Fatalf("a topic never read: ok %v, err %v", ok, err)
	}
	for _, p := range []struct {
		conn, field, topic string
		c                  DecoderChoice
	}{
		{"c1", "value", "orders", DecoderChoice{Name: "JSON"}},
		{"c1", "key", "orders", DecoderChoice{Name: "Text"}},
		// The same topic on another connection is another topic, so its own
		// choice differs from what c1 settles on.
		{"c2", "value", "orders", DecoderChoice{Name: "Text"}},
		{"c1", "value", "payments", DecoderChoice{Name: "Text"}},
		{"c1", "value", "orders", DecoderChoice{Name: "Hex"}}, // chosen again
	} {
		if err := d.PutDecoder(ctx, p.conn, p.field, p.topic, p.c); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		conn, field, topic string
		want               DecoderChoice
	}{
		{"c1", "value", "orders", DecoderChoice{Name: "Hex"}},
		{"c1", "key", "orders", DecoderChoice{Name: "Text"}},
		{"c2", "value", "orders", DecoderChoice{Name: "Text"}},
		{"c1", "value", "payments", DecoderChoice{Name: "Text"}},
	} {
		got, ok, err := d.Decoder(ctx, c.conn, c.field, c.topic)
		if !ok || err != nil || got != c.want {
			t.Errorf("%s %s %s: %+v, %v, %v; want %+v", c.conn, c.field, c.topic, got, ok, err, c.want)
		}
	}
}
