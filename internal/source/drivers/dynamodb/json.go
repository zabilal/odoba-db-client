package dynamodb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// JSON written back into an item.
//
// A map and a list reach the grid as JSON, because a cell has one shape for
// several values, so they come back as JSON and have to become attributes
// again. What is deliberately not done here is guessing a set: a JSON array
// is a list, and a list is what is written, because turning ["a","b"] into a
// string set would change an attribute's type on every edit that touched it —
// and a set cannot be empty, so an emptied one would fail at the service in
// words about the array.

// fromJSON is the attribute a JSON value describes.
func fromJSON(text string) (ddbtypes.AttributeValue, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(text))
	// Numbers as their digits, so that an exact number in a document does not
	// become a float64 on its way through and come back shorter.
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("this is not JSON: %w", err)
	}
	return fromAny(v)
}

func fromAny(v any) (ddbtypes.AttributeValue, error) {
	switch x := v.(type) {
	case nil:
		return &ddbtypes.AttributeValueMemberNULL{Value: true}, nil
	case string:
		return &ddbtypes.AttributeValueMemberS{Value: x}, nil
	case bool:
		return &ddbtypes.AttributeValueMemberBOOL{Value: x}, nil
	case json.Number:
		if _, err := strconv.ParseFloat(x.String(), 64); err != nil {
			return nil, fmt.Errorf("%s is not a number DynamoDB holds: %w", x, err)
		}
		return &ddbtypes.AttributeValueMemberN{Value: x.String()}, nil
	case map[string]any:
		out := make(map[string]ddbtypes.AttributeValue, len(x))
		for name, item := range x {
			av, err := fromAny(item)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			out[name] = av
		}
		return &ddbtypes.AttributeValueMemberM{Value: out}, nil
	case []any:
		out := make([]ddbtypes.AttributeValue, len(x))
		for i, item := range x {
			av, err := fromAny(item)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i+1, err)
			}
			out[i] = av
		}
		return &ddbtypes.AttributeValueMemberL{Value: out}, nil
	}
	return nil, fmt.Errorf("dynamodb cannot hold a %T", v)
}
