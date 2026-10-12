package nats

import (
	"context"
	"errors"
	"fmt"
	"strings"

	natsgo "github.com/nats-io/nats.go"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing one message (FR-13.11).
//
// A message is published to a subject, not to a stream: the stream that keeps
// it is whichever one listens on that subject. So writing "to a stream" needs
// two things said. The subject is what is given as the record's key, which is
// what a subject is shown as when a message is read. And the stream is held to:
// a subject that is not one the stream keeps is refused before anything is
// sent, so that a message cannot be written into one stream by somebody
// looking at a different one, or published to nobody at all.
//
// The server can be asked to hold a message to a stream, and is not, because
// of how it is asked: with a header, and the header is kept with the message.
// Everybody who read it afterwards would find something in it that nobody
// wrote. Two streams cannot keep the same subject, so matching the subject
// here against what the stream keeps decides the same thing and leaves the
// message as it was written.

func (s *natsSource) Produce(ctx context.Context, rec source.ProduceRequest) (_ model.TopicPartition, _ int64, err error) {
	defer panics.Recover(&err, "writing a message")
	none := model.TopicPartition{}

	if err := s.cfg.Guard.Allow(source.AccessWrite, rec.Confirmed); err != nil {
		return none, 0, err
	}
	switch {
	case rec.Topic == "":
		return none, 0, errors.New("nats: a message needs a stream to be written to")
	case rec.Partition > 0:
		return none, 0, fmt.Errorf("nats: a stream is one log, so there is no partition %d to write to", rec.Partition)
	case rec.Subject != "":
		return none, 0, errors.New("nats: there is no schema registry here to check a message against, so nothing was written")
	}
	st, err := s.js.Stream(ctx, rec.Topic)
	if err != nil {
		return none, 0, missing(err, rec.Topic)
	}
	subject, err := subjectFor(string(rec.Key), st.CachedInfo().Config.Subjects, rec.Topic)
	if err != nil {
		return none, 0, err
	}
	msg := &natsgo.Msg{Subject: subject, Data: rec.Value}
	for _, h := range rec.Headers {
		if msg.Header == nil {
			msg.Header = natsgo.Header{}
		}
		msg.Header.Add(h.Key, string(h.Value))
	}
	ack, err := s.js.PublishMsg(ctx, msg)
	if err != nil {
		return none, 0, fmt.Errorf("nats: the message was not written to %s on subject %q: %w", rec.Topic, subject, err)
	}
	return model.TopicPartition{Topic: ack.Stream, Partition: 0}, int64(ack.Sequence), nil
}

// subjectFor is the subject a message is published on: the one given, or the
// stream's own where it has exactly one and that one is a subject rather than
// a pattern. Anything else has to be said, because guessing a subject is
// guessing who receives the message.
func subjectFor(given string, subjects []string, stream string) (string, error) {
	given = strings.TrimSpace(given)
	if given != "" {
		if strings.ContainsAny(given, "*> \t\r\n") {
			return "", fmt.Errorf("nats: %q is a pattern or has a space in it; a message is published on one subject", given)
		}
		for _, kept := range subjects {
			if matches(kept, given) {
				return given, nil
			}
		}
		return "", fmt.Errorf("nats: %q is not a subject stream %s keeps (%s), so nothing was written",
			given, stream, strings.Join(subjects, ", "))
	}
	if len(subjects) == 1 && !strings.ContainsAny(subjects[0], "*>") {
		return subjects[0], nil
	}
	return "", fmt.Errorf("nats: stream %s keeps %s, so the subject to publish on has to be given as the key",
		stream, strings.Join(subjects, ", "))
}

// matches reports whether a subject is one a pattern keeps, by NATS's own
// rules: a subject is words between dots, a star stands for exactly one word,
// and a closing angle bracket for one or more words to the end.
func matches(pattern, subject string) bool {
	want, have := strings.Split(pattern, "."), strings.Split(subject, ".")
	for i, w := range want {
		if w == ">" {
			return i == len(want)-1 && len(have) > i
		}
		if i >= len(have) || have[i] == "" || (w != "*" && w != have[i]) {
			return false
		}
	}
	return len(want) == len(have)
}
