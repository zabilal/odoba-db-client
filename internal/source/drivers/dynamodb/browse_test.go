package dynamodb

import (
	"context"
	"strings"
	"testing"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a browse refuses, and what order it puts things in. Both are decided
// before any call, so both are held here rather than against a service.

// A sort is refused rather than ignored: a scan returns items in the order the
// service finds them, and unsorted items presented as sorted is the failure
// REQ-DRV-3 exists for.
func TestASortIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	s := &dynamoSource{keys: map[string]model.RowIdentity{}}
	_, err := s.Browse(context.Background(), model.NewRef(model.KindCollection, "r", "T"),
		source.BrowseOptions{Sorts: []source.Sort{{Column: "id"}}})
	if err == nil {
		t.Fatal("it sorted a scan")
	}
	if !strings.Contains(err.Error(), "order") {
		t.Errorf("it says %q", err)
	}
}

// Seeking and following are a log's, and a table holds no log.
func TestSeekingAndFollowingAreRefused(t *testing.T) {
	s := &dynamoSource{keys: map[string]model.RowIdentity{}}
	ref := model.NewRef(model.KindCollection, "r", "T")
	for name, opt := range map[string]source.BrowseOptions{
		"a seek":   {Seek: &source.Seek{}},
		"a follow": {Follow: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Browse(context.Background(), ref, opt); err == nil {
				t.Error("it was accepted")
			}
		})
	}
}

// Only a collection holds items, and a browse of anything else is refused
// before a table name is made up from it.
func TestOnlyACollectionIsBrowsed(t *testing.T) {
	s := &dynamoSource{keys: map[string]model.RowIdentity{}}
	if _, err := s.Browse(context.Background(), model.NewRef(model.KindTable, "r", "T"),
		source.BrowseOptions{}); err == nil {
		t.Error("it browsed a table")
	}
}

// The key's attributes come first, because they are what an item is addressed
// by and what somebody reading a row from left to right expects; the rest come
// by name, a table having no column order of its own.
func TestWhatOrderTheAttributesAreShownIn(t *testing.T) {
	for name, c := range map[string]struct {
		keys, seen, want []string
	}{
		"the key, then the rest sorted": {
			[]string{"pk", "sk"}, []string{"zebra", "apple", "sk", "pk", "middle"},
			[]string{"pk", "sk", "apple", "middle", "zebra"}},
		"a key of one": {
			[]string{"pk"}, []string{"b", "a"}, []string{"pk", "a", "b"}},
		// An empty table has no items to sample, and its key is declared all
		// the same.
		"nothing sampled": {[]string{"pk", "sk"}, nil, []string{"pk", "sk"}},
		"no key at all":   {nil, []string{"b", "a"}, []string{"a", "b"}},
		"nothing at all":  {nil, nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := orderedNames(c.keys, c.seen)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("it shows %v, want %v", got, c.want)
			}
		})
	}
}

// A condition the service refuses is not a failure: it is the contract's "no
// row matched", which is what the outcome reads as.
func TestWhatACallsAnswerMeans(t *testing.T) {
	n, err := matched(nil, nil)
	if n != 1 || err != nil {
		t.Errorf("a call that worked reads as %d, %v", n, err)
	}
	n, err = matched(&ddbtypes.ConditionalCheckFailedException{}, nil)
	if n != 0 || err != nil {
		t.Errorf("a refused condition reads as %d, %v", n, err)
	}
	n, err = matched(apiError{code: "ProvisionedThroughputExceededException"}, nil)
	if n != 0 || err == nil {
		t.Errorf("a throttle reads as %d, %v", n, err)
	}
}
