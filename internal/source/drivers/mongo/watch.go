package mongo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Following a collection as it changes (FR-12.5).
//
// A change stream is what MongoDB has instead of a log: it answers when
// somebody writes, for as long as they keep writing, which is exactly what a
// following read is everywhere else here. So it arrives through the same door —
// a browse with Follow set — and the window's Follow control reaches it without
// knowing what kind of source it is talking to (ADR-0164).
//
// What it answers is not documents. A change is an event about a document: what
// happened, to which one, and what the document is now. A deletion has no
// document at all, and a document shown for one would be a document that is not
// there. So a followed collection has its own columns, and the grid reads them
// from the stream as it reads any other (the fetcher's columns are asked for
// every draw).
//
// It needs a replica set or a sharded cluster. A standalone server has no
// oplog, so it cannot answer at all — and it says so in those words rather than
// as the driver's own error, because "the replica set" is not something a person
// browsing a collection is thinking about.

// changeCols are the columns of a followed collection.
//
// Few and fixed, because a change is a shape of its own: what happened is the
// thing being read, and the document it happened to is one value beside it
// rather than a row of fields that differ per event.
var changeCols = []model.ColumnDef{
	{Name: "at", Type: model.DataType{Class: model.TypeTimestamp, Native: "clusterTime", Length: -1}},
	{Name: "change", Type: model.DataType{Class: model.TypeString, Native: "operationType", Length: -1}},
	{Name: "_id", Type: model.DataType{Class: model.TypeString, Native: "documentKey", Length: -1}},
	{Name: "document", Type: model.DataType{Class: model.TypeJSON, Native: "fullDocument", Length: -1}},
	{Name: "fields", Type: model.DataType{Class: model.TypeJSON, Native: "updateDescription", Length: -1}},
}

// CanFollow is a collection and nothing else: a change stream over a whole
// database or deployment is a different object to follow, and following a thing
// that is not the thing on the screen would be a surprise.
//
// The window asks before it offers the control and the driver asks before it
// answers, which is one rule read twice rather than two rules.
func (s *mongoSource) CanFollow(ref model.ObjectRef) bool {
	return ref.Kind == model.KindCollection && len(ref.Path) >= 2
}

// watch opens a change stream over a collection.
func (s *mongoSource) watch(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (_ model.RowStream, err error) {
	defer panics.Recover(&err, "following a collection")
	if !s.CanFollow(ref) {
		return nil, fmt.Errorf("mongodb: %s cannot be followed", ref)
	}
	watch := options.ChangeStream()
	// The document as it is after the change, where the server will give it:
	// without this an update says only which fields moved, and a person
	// watching a collection is watching documents.
	watch.SetFullDocument(options.UpdateLookup)
	if opt.Seek != nil && !opt.Seek.Time.IsZero() {
		// A time is the one position a change stream has: it is the oplog's
		// own, and a time before the oplog begins is refused by the server
		// rather than quietly started from the beginning.
		watch.SetStartAtOperationTime(&bson.Timestamp{T: uint32(opt.Seek.Time.Unix())})
	}
	cs, err := s.collection(ref).Watch(ctx, mongodriver.Pipeline{}, watch)
	if err != nil {
		return nil, followError(err)
	}
	return &changeStream{cs: cs}, nil
}

// followError says what a change stream's refusal means, in the words of what
// somebody was doing rather than the driver's.
func followError(err error) error {
	if err == nil {
		return nil
	}
	var ce mongodriver.CommandError
	if errors.As(err, &ce) &&
		(ce.Code == 40573 || ce.HasErrorMessage("only supported on replica sets")) {
		return errors.New("mongodb: this server keeps no oplog, so a collection " +
			"cannot be followed: that needs a replica set or a sharded cluster")
	}
	// A time before the oplog begins is not among these. It reads as a refusal
	// worth its own words, and no server here gives one: MongoDB 7 starts from
	// the oplog's beginning instead, so a message about it could not be reached.
	return err
}

// changeStream is a followed collection: it answers when somebody writes, and
// never ends, because a collection has not ended.
type changeStream struct {
	cs *mongodriver.ChangeStream
}

func (*changeStream) Columns() []model.ColumnDef { return changeCols }

// Next waits for the next change. It returns io.EOF only when the stream has
// been closed under it: a collection nobody is writing to has not ended, and
// saying that it had would tell the grid a busy collection was empty.
func (c *changeStream) Next(ctx context.Context) (_ model.Row, err error) {
	defer panics.Recover(&err, "reading a change")
	if !c.cs.Next(ctx) {
		if err := c.cs.Err(); err != nil {
			// A read given up on says so through the driver's own error, which
			// carries the cancellation: a tail reads its end from that, and
			// answering ctx.Err() here instead said the same thing twice.
			return nil, followError(err)
		}
		// Next answers false with no error when the stream has been closed,
		// which is the end of it and not a failure.
		return nil, io.EOF
	}
	var ev changeEvent
	if err := c.cs.Decode(&ev); err != nil {
		return nil, err
	}
	return ev.row(), nil
}

func (c *changeStream) Close() error { return c.cs.Close(context.Background()) }

// changeEvent is what a change stream says, of what it says.
type changeEvent struct {
	OperationType string         `bson:"operationType"`
	ClusterTime   bson.Timestamp `bson:"clusterTime"`
	DocumentKey   bson.M         `bson:"documentKey"`
	FullDocument  bson.M         `bson:"fullDocument"`
	UpdateDesc    bson.M         `bson:"updateDescription"`
}

// row is the change as the grid holds it.
func (e changeEvent) row() model.Row {
	row := model.Row{nil, e.OperationType, nil, nil, nil}
	if e.ClusterTime.T != 0 {
		row[0] = time.Unix(int64(e.ClusterTime.T), 0).UTC()
	}
	if id, ok := e.DocumentKey["_id"]; ok {
		// Whatever type the key is: an ObjectID reads as its hex, a string as
		// itself, and anything else as what the grid makes of it. A column of
		// one type could not hold the keys a collection has.
		row[2] = keyText(id)
	}
	if e.FullDocument != nil {
		// A map, as a browsed document is: the grid draws it as compact JSON
		// and the cell viewer opens it in full.
		row[3] = map[string]any(e.FullDocument)
	}
	if e.UpdateDesc != nil {
		row[4] = map[string]any(e.UpdateDesc)
	}
	return row
}

// keyText is a document's key as one column can hold it.
func keyText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bson.ObjectID:
		return t.Hex()
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}
