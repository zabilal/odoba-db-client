package shell

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing a record to a topic (FR-13.11).
//
// This is the only thing the explorer offers that changes data, and it is
// held to what every write is held to: read-only refuses it outright, and a
// connection marked production asks before it happens. The driver refuses
// before it dials, so "nothing has been sent yet" is the literal truth when
// this asks (FR-13.21, FR-4.9).

// selectionProducible reports whether what is selected is a topic on a
// connection that can write records.
func (s *Shell) selectionProducible() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || n.Ref.Kind != model.KindTopic {
		return false
	}
	live, ok := s.d.WS.Get(conn)
	return ok && live.Source.Capabilities().Stream.Produce
}

func (s *Shell) produceToSelected() {
	if conn, n, ok := s.Explorer.SelectedNode(); ok && n.Ref.Kind == model.KindTopic {
		s.produceTo(conn, n.Ref)
	}
}

// produceTo asks what to write, and writes it.
func (s *Shell) produceTo(connID string, ref model.ObjectRef) {
	topic := ref.Name()
	key := widget.NewEntry()
	key.SetPlaceHolder("optional; decides the partition where none is named")
	value := widget.NewMultiLineEntry()
	value.SetPlaceHolder("the record itself")
	value.Wrapping = fyne.TextWrapWord
	partition := widget.NewEntry()
	partition.SetPlaceHolder("any; or a number, to send it to one log")
	headers := widget.NewMultiLineEntry()
	headers.SetPlaceHolder("one per line, written as name: value")
	subject := widget.NewEntry()
	subject.SetPlaceHolder("optional; a registry subject this record must be readable by")

	form := widget.NewForm(
		widget.NewFormItem("Key", key),
		widget.NewFormItem("Value", value),
		widget.NewFormItem("Partition", partition),
		widget.NewFormItem("Headers", headers),
		widget.NewFormItem("Check against", subject),
	)
	d := dialog.NewCustomConfirm("Write a Record to "+topic, "Write", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		req, err := produceFrom(topic, key.Text, value.Text, partition.Text, headers.Text, subject.Text)
		if err != nil {
			s.showError(err)
			return
		}
		s.writeRecord(connID, req)
	}, s.win)
	d.Resize(fyne.NewSize(560, 420))
	d.Show()
}

// writeRecord sends one record, and asks first where the connection says to.
func (s *Shell) writeRecord(connID string, req source.ProduceRequest) {
	go func() {
		live, err := s.d.WS.Connect(s.ctx, connID)
		var at model.TopicPartition
		var offset int64
		if err == nil {
			at, offset, err = app.Produce(s.ctx, live.Source, req)
		}
		s.d.Run(func() {
			switch {
			case errors.Is(err, source.ErrConfirmationRequired):
				s.askBeforeWriting(connID, req)
			case errors.Is(err, source.ErrReadOnly):
				s.showError(errors.New("Not written: this connection is read-only."))
			case err != nil:
				s.showError(fmt.Errorf("could not write the record: %w", err))
			default:
				dialog.ShowInformation("Written",
					fmt.Sprintf("The record is in %s, partition %d, at offset %d.",
						at.Topic, at.Partition, offset), s.win)
			}
		})
	}()
}

// askBeforeWriting is the production guardrail. Nothing has been sent — the
// driver refused before it dialled — so asking and then writing is safe, and
// the consent goes with this record and no other (FR-4.9).
func (s *Shell) askBeforeWriting(connID string, req source.ProduceRequest) {
	c, _ := s.d.Conns.Get(connID)
	d := dialog.NewConfirm("Write to Production?",
		fmt.Sprintf("This writes a record to “%s”, which is marked Production. Nothing has been sent yet.", c.Name),
		func(yes bool) {
			if !yes {
				return
			}
			req.Confirmed = true
			s.writeRecord(connID, req)
		}, s.win)
	d.SetConfirmText("Write")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

// produceFrom reads what was typed into the record it describes.
func produceFrom(topic, key, value, partition, headers, subject string) (source.ProduceRequest, error) {
	req := source.ProduceRequest{
		Topic: topic,
		// Addressed to no partition: the cluster hashes it by key, or spreads
		// it where there is no key. 0 would be a partition, so it cannot mean
		// "any" (produce.go, in the driver).
		Partition: -1,
		Value:     []byte(value),
		Subject:   strings.TrimSpace(subject),
	}
	// A record with no key and one with an empty key are different records:
	// the first is spread across the partitions and the second always hashes
	// to the same one, so an empty box must not become an empty key.
	if key != "" {
		req.Key = []byte(key)
	}
	if p := strings.TrimSpace(partition); p != "" && !strings.EqualFold(p, "any") {
		n, err := strconv.ParseInt(p, 10, 32)
		if err != nil || n < 0 {
			return source.ProduceRequest{}, fmt.Errorf(
				"a partition is a number from 0 upwards, or nothing at all for any: %q", partition)
		}
		req.Partition = int32(n)
	}
	hs, err := headersFrom(headers)
	if err != nil {
		return source.ProduceRequest{}, err
	}
	req.Headers = hs
	return req, nil
}

// headersFrom reads one header per line, written as name: value.
//
// The first colon separates, because a header's value may hold one — a trace
// parent and a URL both do — and a name may not.
func headersFrom(text string) ([]model.RecordHeader, error) {
	var out []model.RecordHeader
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, val, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("header %d is %q, which is not a name and a value with a colon between them", i+1, line)
		}
		if name = strings.TrimSpace(name); name == "" {
			return nil, fmt.Errorf("header %d has a value and no name to go with it", i+1)
		}
		out = append(out, model.RecordHeader{Key: name, Value: []byte(strings.TrimSpace(val))})
	}
	return out, nil
}
