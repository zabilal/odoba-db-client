package localdb

import (
	"context"
	"encoding/json"
)

// ParamValue is a query parameter's value as last given (FR-5.7): its text,
// or NULL.
type ParamValue struct {
	Text string `json:"text,omitempty"`
	Null bool   `json:"null,omitempty"`
}

// Parameter values live in the key-value table under this prefix, one key
// to a connection and name. Neither holds a '/': a connection's ID is hex,
// and a name is an identifier.
const paramPrefix = "param/"

func paramKey(connID, name string) string { return paramPrefix + connID + "/" + name }

// Param is the value last given to a named parameter on a connection, and
// whether one was.
func (d *DB) Param(ctx context.Context, connID, name string) (ParamValue, bool, error) {
	b, ok, err := d.Get(ctx, paramKey(connID, name))
	if err != nil || !ok {
		return ParamValue{}, false, err
	}
	var v ParamValue
	if err := json.Unmarshal(b, &v); err != nil {
		return ParamValue{}, false, err
	}
	return v, true, nil
}

// PutParam remembers the value given to a named parameter on a connection.
func (d *DB) PutParam(ctx context.Context, connID, name string, v ParamValue) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return d.Put(ctx, paramKey(connID, name), b)
}
