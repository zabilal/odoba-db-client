package dynamodb

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The tree (FR-2.1, FR-2.2).
//
// A connection is to one region, and a region is the container: there is
// nothing above a table to choose between, so the tree is region → the
// Collections class → the tables, and a table opens onto its indexes.
//
// A DynamoDB table is a KindCollection rather than a KindTable, which is the
// model's decision and not this driver's: an object's kind is what it is
// rather than what its engine calls it, and a set of items with no declared
// shape is what the model calls a collection. The class label follows from the
// kind and is the same on every engine, which is the point (REQ-DB-4).

// listAll is how many table names are asked for at a time. The service's own
// maximum is a hundred.
const listAll = 100

// Root is the region's classes.
func (s *dynamoSource) Root(ctx context.Context) ([]model.Node, error) {
	names, err := s.tables(ctx)
	if err != nil {
		return nil, err
	}
	region := model.NewRef(model.KindDatabase, s.region)
	out := model.ClassNodes(region, map[model.ObjectKind]int64{
		model.KindCollection: int64(len(names)),
	})
	if len(out) == 0 {
		// A region with nothing in it still shows its collections, empty: a
		// node that opens onto nothing reads as a tree that failed rather
		// than a region that is empty.
		out = []model.Node{model.ClassNode(region, model.KindCollection, 0)}
	}
	return out, nil
}

func (s *dynamoSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return s.Root(ctx)
	case model.KindFolder:
		kind, ok := model.ClassOf(ref)
		if !ok {
			return nil, fmt.Errorf("dynamodb: no such class %s", ref)
		}
		switch kind {
		case model.KindCollection:
			return s.collections(ctx)
		case model.KindIndex:
			return s.indexNodes(ctx, ref)
		}
		return nil, nil
	case model.KindCollection:
		return s.collectionClasses(ctx, ref)
	}
	return nil, nil
}

// tables is every table in the region, in name order, which is the order the
// service lists them in.
func (s *dynamoSource) tables(ctx context.Context) ([]string, error) {
	in := &dynamodb.ListTablesInput{Limit: aws.Int32(listAll)}
	var out []string
	for {
		page, err := s.client.ListTables(ctx, in)
		if err != nil {
			return nil, statementError(err, ctx)
		}
		out = append(out, page.TableNames...)
		if page.LastEvaluatedTableName == nil {
			return out, nil
		}
		in.ExclusiveStartTableName = page.LastEvaluatedTableName
	}
}

// collections lists the region's tables.
//
// Whether a table opens onto anything is whether it has a secondary index,
// and the only way to know is to describe it: there is no call that lists the
// indexes of a region. So the tables are described, which is one request each
// — the cost of the tree telling the truth about which nodes open. They go out
// at once, up to describeAtOnce of them, so the list waits for the slowest few
// rather than for all of them in turn.
//
// A table that cannot be described is shown without children rather than left
// out: listing a table and describing it are separate permissions in IAM, and
// a person who may do the first should still see what is there.
func (s *dynamoSource) collections(ctx context.Context) ([]model.Node, error) {
	names, err := s.tables(ctx)
	if err != nil {
		return nil, err
	}
	indexed, err := s.indexed(ctx, names)
	if err != nil {
		return nil, err
	}
	out := make([]model.Node, 0, len(names))
	for _, name := range names {
		out = append(out, model.Node{
			Ref: model.NewRef(model.KindCollection, s.region, name), Label: name,
			HasChildren: indexed[name], Browsable: true,
		})
	}
	return out, nil
}

// describeAtOnce is how many tables are described at a time. Enough that a
// region of a hundred tables is a dozen round trips of waiting rather than a
// hundred; few enough not to be the thing that gets an identity throttled.
const describeAtOnce = 8

// indexed says, for each table, whether it has a secondary index.
func (s *dynamoSource) indexed(ctx context.Context, names []string) (map[string]bool, error) {
	out := make(map[string]bool, len(names))
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, describeAtOnce)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			break
		}
		wg.Add(1)
		slots <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-slots }()
			desc, err := s.describe(ctx, model.NewRef(model.KindCollection, s.region, name))
			if err != nil {
				return // shown without children; see the note above
			}
			has := len(desc.GlobalSecondaryIndexes)+len(desc.LocalSecondaryIndexes) > 0
			mu.Lock()
			out[name] = has
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	return out, ctx.Err()
}

// collectionClasses are the classes a table holds: its indexes, where it has
// any.
func (s *dynamoSource) collectionClasses(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	desc, err := s.describe(ctx, ref)
	if err != nil {
		return nil, err
	}
	n := int64(len(desc.GlobalSecondaryIndexes) + len(desc.LocalSecondaryIndexes))
	if n == 0 {
		return nil, nil
	}
	return model.ClassNodes(ref, map[model.ObjectKind]int64{model.KindIndex: n}), nil
}

// indexNodes lists a table's secondary indexes, global and local together:
// both are an index of the table, and which kind it is is what the node says
// about it rather than which list it is in.
func (s *dynamoSource) indexNodes(ctx context.Context, class model.ObjectRef) ([]model.Node, error) {
	table := class.Parent(model.KindCollection)
	desc, err := s.describe(ctx, table)
	if err != nil {
		return nil, err
	}
	var out []model.Node
	for _, ix := range indexesOf(desc) {
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindIndex, s.region, table.Name(), ix.Name),
			Label: ix.Name, Attrs: ix.Attrs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// describe is the service's description of a table.
func (s *dynamoSource) describe(ctx context.Context, ref model.ObjectRef) (*ddbtypes.TableDescription, error) {
	table, err := tableOf(ref)
	if err != nil {
		return nil, err
	}
	out, err := s.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		return nil, statementError(err, ctx)
	}
	if out.Table == nil {
		return nil, fmt.Errorf("dynamodb: the service described %s as nothing", table)
	}
	return out.Table, nil
}

// Describe loads a table's structure: its key, its indexes, and the numbers
// the service keeps about it. The shape of its items is not here — inference
// is always explicit and cancellable, never a side effect of opening a node
// (FR-12.4).
func (s *dynamoSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if ref.Kind != model.KindCollection {
		return nil, fmt.Errorf("dynamodb: cannot describe %s", ref)
	}
	desc, err := s.describe(ctx, ref)
	if err != nil {
		return nil, err
	}
	coll := &model.Collection{
		Name:              aws.ToString(desc.TableName),
		Indexes:           indexesOf(desc),
		DocumentsEstimate: -1,
		Attrs:             attrsOf(desc),
	}
	if desc.ItemCount != nil {
		coll.DocumentsEstimate = *desc.ItemCount
	}
	return coll, nil
}

// indexesOf is a table's secondary indexes. A DynamoDB index is not a
// uniqueness constraint and is not sparse in the model's sense — it holds the
// items whose key attributes are present, which is closer to a partial index,
// and what it projects is the thing worth saying about it.
func indexesOf(desc *ddbtypes.TableDescription) []model.DocumentIndex {
	var out []model.DocumentIndex
	for _, ix := range desc.GlobalSecondaryIndexes {
		out = append(out, model.DocumentIndex{
			Name:  aws.ToString(ix.IndexName),
			Keys:  indexKeys(ix.KeySchema),
			Attrs: indexAttrs("global", ix.Projection, ix.ItemCount, ix.IndexSizeBytes, string(ix.IndexStatus)),
		})
	}
	for _, ix := range desc.LocalSecondaryIndexes {
		out = append(out, model.DocumentIndex{
			Name:  aws.ToString(ix.IndexName),
			Keys:  indexKeys(ix.KeySchema),
			Attrs: indexAttrs("local", ix.Projection, ix.ItemCount, ix.IndexSizeBytes, ""),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// indexKeys are an index's key attributes, the partition key first.
func indexKeys(schema []ddbtypes.KeySchemaElement) []model.IndexColumn {
	out := make([]model.IndexColumn, 0, len(schema))
	for _, name := range keyColumns(schema) {
		out = append(out, model.IndexColumn{Name: name})
	}
	return out
}

func indexAttrs(kind string, projection *ddbtypes.Projection, items, bytes *int64, status string) map[string]string {
	attrs := map[string]string{"kind": kind}
	if projection != nil {
		attrs["projects"] = projects(projection)
	}
	if items != nil {
		attrs["items"] = strconv.FormatInt(*items, 10)
	}
	if bytes != nil {
		attrs["size"] = strconv.FormatInt(*bytes, 10)
	}
	if status != "" && status != string(ddbtypes.IndexStatusActive) {
		// An index still being built answers a query with part of the table,
		// which is worth knowing before somebody reads one and believes it.
		attrs["status"] = status
	}
	return attrs
}

// projects is what an index carries, in words: an index that projects only
// its keys answers a query about anything else by going back to the table, or
// not at all, which is the commonest surprise about a DynamoDB index.
func projects(p *ddbtypes.Projection) string {
	switch p.ProjectionType {
	case ddbtypes.ProjectionTypeAll:
		return "every attribute"
	case ddbtypes.ProjectionTypeKeysOnly:
		return "the keys only"
	case ddbtypes.ProjectionTypeInclude:
		return "the keys and " + join(p.NonKeyAttributes, ", ")
	}
	return string(p.ProjectionType)
}

// attrsOf is what the service says about a table beyond its shape.
func attrsOf(desc *ddbtypes.TableDescription) map[string]string {
	attrs := map[string]string{}
	if status := string(desc.TableStatus); status != "" && status != string(ddbtypes.TableStatusActive) {
		attrs["status"] = status
	}
	for _, name := range keyColumns(desc.KeySchema) {
		if attrs["key"] == "" {
			attrs["key"] = name
			continue
		}
		attrs["key"] += ", " + name
	}
	if desc.BillingModeSummary != nil && desc.BillingModeSummary.BillingMode != "" {
		attrs["billing"] = string(desc.BillingModeSummary.BillingMode)
	}
	if desc.TableSizeBytes != nil {
		attrs["size"] = strconv.FormatInt(*desc.TableSizeBytes, 10)
	}
	if desc.CreationDateTime != nil {
		attrs["created"] = desc.CreationDateTime.UTC().Format(time.RFC3339)
	}
	if desc.StreamSpecification != nil && aws.ToBool(desc.StreamSpecification.StreamEnabled) {
		attrs["stream"] = string(desc.StreamSpecification.StreamViewType)
	}
	if aws.ToBool(desc.DeletionProtectionEnabled) {
		attrs["deletion protection"] = "on"
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// Badge is a table's item count, which the service keeps as metadata and does
// not count for: it is up to six hours old, and says so by not claiming to be
// exact (FR-2.5).
func (s *dynamoSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	if ref.Kind != model.KindCollection {
		return model.Badge{}, false, nil
	}
	desc, err := s.describe(ctx, ref)
	if err != nil {
		return model.Badge{}, false, err
	}
	if desc.ItemCount == nil {
		return model.Badge{}, false, nil
	}
	return model.Badge{Text: "~" + strconv.FormatInt(*desc.ItemCount, 10), Exact: false}, true, nil
}
