package app

import (
	"context"
	"errors"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A cluster's permissions (FR-13.15).
//
// Reading them and changing them are two claims, so they are two interfaces
// and two refusals here (ADR-0107). Changing one is AccessAdmin, refused by
// the driver before it dials where the connection is read-only or wants asking
// (FR-13.21). None of these relaxes that; they are the calls, not the guard.

// ACLs are the permissions matching of. The zero filter asks for all of them.
func ACLs(ctx context.Context, src source.Source, of model.ACLFilter) (_ []model.ACL, err error) {
	defer panics.Recover(&err, "reading the permissions")
	a, ok := src.(source.ACLInspector)
	if !ok {
		return nil, errNoACLs
	}
	return a.ACLs(ctx, of)
}

// GrantACL adds one permission.
func GrantACL(ctx context.Context, src source.Source, acl model.ACL, confirmed bool) (err error) {
	defer panics.Recover(&err, "granting a permission")
	a, ok := src.(source.ACLAdmin)
	if !ok {
		return errNoACLAdmin
	}
	return a.GrantACL(ctx, acl, confirmed)
}

// RevokeACL takes one away.
func RevokeACL(ctx context.Context, src source.Source, acl model.ACL, confirmed bool) (err error) {
	defer panics.Recover(&err, "revoking a permission")
	a, ok := src.(source.ACLAdmin)
	if !ok {
		return errNoACLAdmin
	}
	return a.RevokeACL(ctx, acl, confirmed)
}

var (
	errNoACLs     = errors.New("this source keeps no permissions to read")
	errNoACLAdmin = errors.New("this source cannot grant or revoke permissions")
)
