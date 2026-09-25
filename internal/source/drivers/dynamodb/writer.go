package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Writing items (FR-4.4, FR-4.5, FR-12.1).
//
// A plan is the calls that would be made, rendered as a person reads them, and
// carried beside them in the form the SDK takes. The rules for what the
// outcome means are the contract's: a change that matches no item is an item
// changed or deleted since it was read, and the shared ApplyWith reads them.
//
// Every write carries a condition, which is the point of this file. DynamoDB's
// PutItem and UpdateItem both create the item when it is not there, so a
// change to an item somebody else has deleted would silently put it back, and
// an insert of an item whose key is taken would silently overwrite it. Neither
// is what the grid means, and the contract says so: a change addresses one row
// that was read, and a new row is new. So an insert asks for the key not to
// exist, and a change and a delete ask for it to exist, and a condition the
// service refuses becomes the contract's "no row matched".

var _ source.Writer = (*dynamoSource)(nil)

// Plan renders a changeset as the writes it would make.
func (s *dynamoSource) Plan(ctx context.Context, cs source.Changeset) (*source.WritePlan, error) {
	table, err := tableOf(cs.Target)
	if err != nil {
		return nil, err
	}
	// Every item is addressed by the table's key, which is declared. A
	// changeset keyed by anything else was made somewhere else, and an
	// UpdateItem with the wrong key would create an item rather than change
	// one.
	id, err := s.identity(ctx, cs.Target)
	if err != nil {
		return nil, err
	}
	if !insertsOnly(cs.Changes) {
		if !id.Editable() {
			return nil, errors.New("these items have no key to tell them apart, so they cannot be written")
		}
		if !sameKey(cs.Identity.Columns, id.Columns) {
			return nil, fmt.Errorf("an item of %s is addressed by %s, and these changes are keyed by %s",
				table, strings.Join(id.Columns, ", "), strings.Join(cs.Identity.Columns, ", "))
		}
	}
	plan := &source.WritePlan{
		Target: cs.Target,
		// Items are written one at a time, so the writes before a failure
		// stand and the person is told so before committing (FR-4.5).
		Atomic:  false,
		Guarded: s.cfg.Guard.RequiresConfirmation(source.AccessWrite),
	}
	for i, c := range cs.Changes {
		st, desc, err := writeOf(table, id.Columns, c)
		if err != nil {
			return nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		st.Confirmed = cs.Confirmed
		plan.Statements = append(plan.Statements, st)
		plan.Descriptions = append(plan.Descriptions, desc)
	}
	return plan, nil
}

// Apply makes a plan's writes in order. Nothing is undone on a failure: these
// are separate calls to a service with no transaction across them, and the
// outcome says which had already been made.
func (s *dynamoSource) Apply(ctx context.Context, plan *source.WritePlan) (*source.WriteOutcome, error) {
	if err := sqlscript.AllowWrites(s.cfg.Guard, plan); err != nil {
		return nil, err
	}
	if _, err := tableOf(plan.Target); err != nil {
		return nil, err
	}
	return sqlscript.ApplyWith(plan, func(st source.Statement) (int64, error) {
		// The call is the statement's own; a plan carrying none was made
		// somewhere else, and nothing of it is run.
		op, ok := st.Op.(*itemWrite)
		if !ok {
			return 0, errors.New("dynamodb: this plan was not made here")
		}
		return op.run(ctx, s.client)
	}, func() error { return nil }, func() error {
		// Nothing was undone, and the outcome must not say it was.
		return errNoRollback
	}), nil
}

var errNoRollback = errors.New("dynamodb: these are separate calls to the service, so the writes before the failure stand")

func insertsOnly(changes []source.RowChange) bool {
	for _, c := range changes {
		if c.Kind != source.ChangeInsert {
			return false
		}
	}
	return true
}

func sameKey(given, want []string) bool {
	if len(given) != len(want) {
		return false
	}
	for i := range want {
		if given[i] != want[i] {
			return false
		}
	}
	return true
}

// itemWrite is one call, in the form the SDK takes it.
type itemWrite struct {
	kind  source.ChangeKind
	table string
	key   map[string]ddbtypes.AttributeValue
	item  map[string]ddbtypes.AttributeValue
	set   []string
	gone  []string
	names map[string]string
	vals  map[string]ddbtypes.AttributeValue
	cond  string
}

// run makes the call and returns how many items it changed: one, or none where
// the condition was not met, which is what ApplyWith reads as an item changed
// or deleted since it was read.
func (w *itemWrite) run(ctx context.Context, c *dynamodb.Client) (int64, error) {
	var err error
	switch w.kind {
	case source.ChangeInsert:
		_, err = c.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(w.table), Item: w.item,
			ConditionExpression:      condOrNil(w.cond),
			ExpressionAttributeNames: namesOrNil(w.names),
		})
	case source.ChangeUpdate:
		_, err = c.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: aws.String(w.table), Key: w.key,
			UpdateExpression:          aws.String(w.updateExpression()),
			ConditionExpression:       condOrNil(w.cond),
			ExpressionAttributeNames:  namesOrNil(w.names),
			ExpressionAttributeValues: valuesOrNil(w.vals),
		})
	case source.ChangeDelete:
		_, err = c.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String(w.table), Key: w.key,
			ConditionExpression:      condOrNil(w.cond),
			ExpressionAttributeNames: namesOrNil(w.names),
		})
	default:
		return 0, fmt.Errorf("dynamodb: a change of unknown kind %d", w.kind)
	}
	return matched(err, ctx)
}

// matched reads what a call answered as how many items it changed: one, or
// none where the condition was not met, which is what ApplyWith reads as an
// item changed or deleted since it was read.
//
// The condition not being met is not a failure and must not be reported as
// one: for a change or a delete the item has gone, and for an insert its key
// is taken, and both are things the grid has words for.
func matched(err error, ctx context.Context) (int64, error) {
	var refused *ddbtypes.ConditionalCheckFailedException
	if errors.As(err, &refused) {
		return 0, nil
	}
	if err != nil {
		return 0, statementError(err, ctx)
	}
	return 1, nil
}

// updateExpression is the SET and REMOVE of a change, in that order, which is
// the order DynamoDB requires.
func (w *itemWrite) updateExpression() string {
	var parts []string
	if len(w.set) > 0 {
		parts = append(parts, "SET "+strings.Join(w.set, ", "))
	}
	if len(w.gone) > 0 {
		parts = append(parts, "REMOVE "+strings.Join(w.gone, ", "))
	}
	return strings.Join(parts, " ")
}

func condOrNil(cond string) *string {
	if cond == "" {
		return nil
	}
	return aws.String(cond)
}

func namesOrNil(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func valuesOrNil(m map[string]ddbtypes.AttributeValue) map[string]ddbtypes.AttributeValue {
	if len(m) == 0 {
		return nil
	}
	return m
}

// writeOf renders one change, and says in a line what it does.
func writeOf(table string, keyColumns []string, c source.RowChange) (source.Statement, string, error) {
	names := make([]string, 0, len(c.Values))
	for name := range c.Values {
		names = append(names, name)
	}
	sort.Strings(names)

	e := &expression{}
	w := &itemWrite{kind: c.Kind, table: table}
	// The condition is on the partition key, which every item has and which is
	// the cheapest thing to test.
	exists := func() string {
		if len(keyColumns) == 0 {
			return ""
		}
		return "attribute_exists(" + e.name(keyColumns[0]) + ")"
	}

	key := func() (string, error) {
		if len(c.Key) != len(keyColumns) {
			return "", fmt.Errorf("the key has %d values for %d attributes", len(c.Key), len(keyColumns))
		}
		w.key = make(map[string]ddbtypes.AttributeValue, len(keyColumns))
		said := make([]string, len(keyColumns))
		for i, name := range keyColumns {
			if c.Key[i] == nil {
				return "", fmt.Errorf("the item's %s is empty, which addresses no item", name)
			}
			av, err := attributeOf(c.Key[i])
			if err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
			w.key[name] = av
			said[i] = fmt.Sprintf("%s = %v", name, c.Key[i])
		}
		return strings.Join(said, ", "), nil
	}

	switch c.Kind {
	case source.ChangeUpdate:
		if len(names) == 0 {
			return source.Statement{}, "", errors.New("an update that changes nothing")
		}
		said, err := key()
		if err != nil {
			return source.Statement{}, "", err
		}
		for _, name := range names {
			if _, gone := c.Values[name].(model.Removed); gone {
				w.gone = append(w.gone, e.name(name))
				continue
			}
			mark, err := e.value(c.Values[name])
			if err != nil {
				return source.Statement{}, "", fmt.Errorf("%s: %w", name, err)
			}
			w.set = append(w.set, e.name(name)+" = "+mark)
		}
		w.cond = exists()
		w.names, w.vals = e.names, e.values
		st := source.Statement{
			SQL: fmt.Sprintf("UpdateItem on %s where %s: %s", table, said, w.updateExpression()),
			Op:  w,
		}
		return st, "Change " + strings.Join(names, ", ") + " in the item where " + said, nil

	case source.ChangeDelete:
		said, err := key()
		if err != nil {
			return source.Statement{}, "", err
		}
		w.cond = exists()
		w.names = e.names
		st := source.Statement{SQL: fmt.Sprintf("DeleteItem on %s where %s", table, said), Op: w}
		return st, "Delete the item where " + said, nil

	case source.ChangeInsert:
		w.item = make(map[string]ddbtypes.AttributeValue, len(names))
		for _, name := range names {
			if _, gone := c.Values[name].(model.Removed); gone {
				continue // a new item simply does not have it
			}
			if _, dflt := c.Values[name].(model.Default); dflt {
				// Nothing here fills a value in: there is no default in a
				// store with no declared shape, and an attribute nobody gave
				// a value to is an attribute the item has not got.
				continue
			}
			av, err := attributeOf(c.Values[name])
			if err != nil {
				return source.Statement{}, "", fmt.Errorf("%s: %w", name, err)
			}
			w.item[name] = av
		}
		for _, name := range keyColumns {
			if _, ok := w.item[name]; !ok {
				return source.Statement{}, "", fmt.Errorf(
					"a new item needs a value for %s, which is what addresses it", name)
			}
		}
		// A new item is new: its key must not be taken. Without this,
		// PutItem would overwrite whatever is there and report success.
		if len(keyColumns) > 0 {
			w.cond = "attribute_not_exists(" + e.name(keyColumns[0]) + ")"
			w.names = e.names
		}
		st := source.Statement{
			SQL: fmt.Sprintf("PutItem on %s: %s", table, strings.Join(names, ", ")),
			Op:  w,
		}
		return st, "Add an item: " + strings.Join(names, ", "), nil
	}
	return source.Statement{}, "", fmt.Errorf("a change of unknown kind %d", c.Kind)
}
