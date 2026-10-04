package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A cluster's permissions (T5.13, FR-13.15).
//
// Reading them is a claim of its own and changing them is another, which is
// why they are two interfaces (ADR-0107). Changing one is AccessAdmin and
// passes the guard before a broker is asked anything: a read-only connection
// refuses outright, and a production one refuses without consent given for
// this act and no other (FR-4.9).
//
// A cluster with no authorizer configured holds no permissions at all. It says
// so in those words rather than answering an empty list, because an empty list
// reads as "nobody may do anything" where the truth is the opposite: everybody
// who can reach the cluster may do everything.

// aclKinds are the resources a permission can be about, in this application's
// words and the protocol's.
var aclKinds = []struct {
	said model.ACLResourceKind
	wire kmsg.ACLResourceType
}{
	{model.ACLTopic, kmsg.ACLResourceTypeTopic},
	{model.ACLGroup, kmsg.ACLResourceTypeGroup},
	{model.ACLClusterItself, kmsg.ACLResourceTypeCluster},
	{model.ACLTransactionalID, kmsg.ACLResourceTypeTransactionalId},
	{model.ACLDelegationToken, kmsg.ACLResourceTypeDelegationToken},
	{model.ACLUserPrincipal, kmsg.ACLResourceTypeUser},
}

// aclOps are the operations, likewise.
var aclOps = []struct {
	said model.ACLOperation
	wire kadm.ACLOperation
}{
	{model.ACLRead, kadm.OpRead},
	{model.ACLWrite, kadm.OpWrite},
	{model.ACLCreate, kadm.OpCreate},
	{model.ACLDelete, kadm.OpDelete},
	{model.ACLAlter, kadm.OpAlter},
	{model.ACLDescribe, kadm.OpDescribe},
	{model.ACLAlterConfigs, kadm.OpAlterConfigs},
	{model.ACLDescribeConfigs, kadm.OpDescribeConfigs},
	{model.ACLClusterAction, kadm.OpClusterAction},
	{model.ACLIdempotentWrite, kadm.OpIdempotentWrite},
	{model.ACLAll, kadm.OpAll},
}

// clusterResource is the name Kafka gives the cluster itself. A person does
// not name the cluster they are connected to, so the model leaves it out and
// this puts it back.
const clusterResource = "kafka-cluster"

// anyName is how the protocol writes "every object of the kind".
const anyName = "*"

// kindSaid is the word for a resource type. A type this application has no
// word for is still shown, in the protocol's own word with its underscores
// taken out: a permission nobody can see is worse than one named oddly.
func kindSaid(t kmsg.ACLResourceType) model.ACLResourceKind {
	for _, k := range aclKinds {
		if k.wire == t {
			return k.said
		}
	}
	return model.ACLResourceKind(plainly(t.String()))
}

// opSaid is the word for an operation, on the same terms.
func opSaid(op kadm.ACLOperation) model.ACLOperation {
	for _, o := range aclOps {
		if o.wire == op {
			return o.said
		}
	}
	return model.ACLOperation(plainly(op.String()))
}

// plainly is a protocol word as a person would read it.
func plainly(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", " "))
}

// kindWire is the protocol's type for a kind. A kind it does not know is
// refused: a permission granted about something other than what was asked for
// would be a permission nobody meant to give.
func kindWire(k model.ACLResourceKind) (kmsg.ACLResourceType, error) {
	for _, c := range aclKinds {
		if c.said == k {
			return c.wire, nil
		}
	}
	return 0, fmt.Errorf("kafka: a permission cannot be about a %q", k)
}

// opWire is the protocol's operation, on the same terms.
func opWire(op model.ACLOperation) (kadm.ACLOperation, error) {
	for _, c := range aclOps {
		if c.said == op {
			return c.wire, nil
		}
	}
	return 0, fmt.Errorf("kafka: %q is not something a permission can allow", op)
}

// ACLs reads the permissions matching of.
func (s *kafkaSource) ACLs(ctx context.Context, of model.ACLFilter) (_ []model.ACL, err error) {
	defer panics.Recover(&err, "reading the permissions")

	b, err := aclFilter(of)
	if err != nil {
		return nil, err
	}
	results, err := s.admin.DescribeACLs(ctx, b)
	if err != nil {
		return nil, aclError(err)
	}
	var out []model.ACL
	for _, r := range results {
		if r.Err != nil {
			return nil, aclError(said(r.Err, r.ErrMessage))
		}
		for _, d := range r.Described {
			out = append(out, aclSaid(d))
		}
	}
	sortACLs(out)
	return out, nil
}

// GrantACL adds one permission.
func (s *kafkaSource) GrantACL(ctx context.Context, acl model.ACL, confirmed bool) (err error) {
	defer panics.Recover(&err, "granting a permission")

	if err := s.cfg.Guard.Allow(source.AccessAdmin, confirmed); err != nil {
		return err
	}
	b, err := aclExactly(acl)
	if err != nil {
		return err
	}
	results, err := s.admin.CreateACLs(ctx, b)
	if err != nil {
		return aclError(err)
	}
	for _, r := range results {
		if r.Err != nil {
			return aclError(said(r.Err, r.ErrMessage))
		}
	}
	return nil
}

// RevokeACL takes one away.
func (s *kafkaSource) RevokeACL(ctx context.Context, acl model.ACL, confirmed bool) (err error) {
	defer panics.Recover(&err, "revoking a permission")

	if err := s.cfg.Guard.Allow(source.AccessAdmin, confirmed); err != nil {
		return err
	}
	b, err := aclExactly(acl)
	if err != nil {
		return err
	}
	results, err := s.admin.DeleteACLs(ctx, b)
	if err != nil {
		return aclError(err)
	}
	gone := 0
	for _, r := range results {
		if r.Err != nil {
			return aclError(said(r.Err, r.ErrMessage))
		}
		gone += len(r.Deleted)
	}
	if gone == 0 {
		// Saying nothing here would report success for a permission that is
		// still there, or that somebody else took away first.
		return fmt.Errorf("kafka: there is no such permission to revoke: %s", acl)
	}
	return nil
}

// aclError says what a refusal about permissions means.
func aclError(err error) error {
	if errors.Is(err, kerr.SecurityDisabled) {
		return errors.New("kafka: this cluster keeps no permissions: no authorizer is " +
			"configured on the brokers, so everything is allowed to everybody who can reach it")
	}
	return err
}

// aclFilter is the builder for a listing: every field the filter leaves empty
// matches anything.
func aclFilter(of model.ACLFilter) (*kadm.ACLBuilder, error) {
	b := kadm.NewACLs().Operations()
	// Both permissions, because a listing that showed only what is allowed
	// would hide the denials that overrule them.
	if of.Principal == "" {
		b.Allow().AllowHosts().Deny().DenyHosts()
	} else {
		b.Allow(of.Principal).AllowHosts().Deny(of.Principal).DenyHosts()
	}
	switch {
	case of.Kind == "":
		b.AnyResource()
	case of.Kind == model.ACLClusterItself:
		b.Clusters()
	default:
		wire, err := kindWire(of.Kind)
		if err != nil {
			return nil, err
		}
		if err := named(b, wire, of.Name); err != nil {
			return nil, err
		}
	}
	if of.Name == "" {
		// Any name, written any way. The cluster is here too: it has no name to
		// give, so a filter about it never has one.
		b.ResourcePatternType(kadm.ACLPatternAny)
	} else {
		// Everything that applies to this one object: the permission written
		// about its name, the one written about every name of its kind, and
		// any prefix of it. All three reach it, so all three are what somebody
		// asking about it is asking for.
		b.ResourcePatternType(kadm.ACLPatternMatch)
	}
	if err := b.ValidateFilter(); err != nil {
		return nil, err
	}
	return b, nil
}

// aclExactly is the builder for one permission as it is written — never a
// pattern, because a revoke by pattern would take away permissions nobody
// named, and a grant by one would give more than was asked for.
func aclExactly(acl model.ACL) (*kadm.ACLBuilder, error) {
	if strings.TrimSpace(acl.Principal) == "" {
		return nil, errors.New("kafka: a permission needs somebody it is about")
	}
	if acl.Resource.Kind == model.ACLClusterItself && acl.Resource.Prefixed {
		return nil, errors.New("kafka: there is one cluster, so a permission about it " +
			"cannot be about names beginning with something")
	}
	op, err := opWire(acl.Operation)
	if err != nil {
		return nil, err
	}
	wire, err := kindWire(acl.Resource.Kind)
	if err != nil {
		return nil, err
	}
	host := acl.Host
	if host == "" {
		host = anyName
	}
	b := kadm.NewACLs().Operations(op)
	if acl.Deny {
		b.Deny(acl.Principal).DenyHosts(host)
	} else {
		b.Allow(acl.Principal).AllowHosts(host)
	}
	if acl.Resource.Kind == model.ACLClusterItself {
		b.Clusters()
	} else if err := named(b, wire, acl.Resource.Name); err != nil {
		return nil, err
	}
	if acl.Resource.Prefixed {
		b.ResourcePatternType(kadm.ACLPatternPrefixed)
	}
	if err := b.ValidateCreate(); err != nil {
		return nil, err
	}
	return b, nil
}

// named puts a resource of one kind into a builder. An empty name is every
// object of the kind, which the protocol writes as a star.
func named(b *kadm.ACLBuilder, wire kmsg.ACLResourceType, name string) error {
	if name == "" {
		name = anyName
	}
	switch wire {
	case kmsg.ACLResourceTypeTopic:
		b.Topics(name)
	case kmsg.ACLResourceTypeGroup:
		b.Groups(name)
	case kmsg.ACLResourceTypeTransactionalId:
		b.TransactionalIDs(name)
	case kmsg.ACLResourceTypeDelegationToken:
		b.DelegationTokens(name)
	default:
		// A user principal, and anything a later protocol adds: kadm names the
		// resources it can build, and this is not one of them.
		return fmt.Errorf("kafka: this client cannot ask about permissions on a %s", plainly(wire.String()))
	}
	return nil
}

// aclSaid is a described permission as this application holds it.
func aclSaid(d kadm.DescribedACL) model.ACL {
	acl := model.ACL{
		Principal: d.Principal,
		Host:      d.Host,
		Resource: model.ACLResource{
			Kind:     kindSaid(d.Type),
			Name:     d.Name,
			Prefixed: d.Pattern == kadm.ACLPatternPrefixed,
		},
		Operation: opSaid(d.Operation),
		Deny:      d.Permission == kmsg.ACLPermissionTypeDeny,
	}
	if acl.Resource.Name == anyName || acl.Resource.Kind == model.ACLClusterItself {
		// Every object of the kind, and the one cluster: neither is a name a
		// person gave, so neither is shown as one.
		acl.Resource.Name = ""
	}
	return acl
}

// sortACLs puts them in an order that does not move between two readings of
// the same cluster: by what they are about, then who they are about.
func sortACLs(acls []model.ACL) {
	sort.SliceStable(acls, func(i, j int) bool {
		a, b := acls[i], acls[j]
		switch {
		case a.Resource.Kind != b.Resource.Kind:
			return a.Resource.Kind < b.Resource.Kind
		case a.Resource.Name != b.Resource.Name:
			return a.Resource.Name < b.Resource.Name
		case a.Resource.Prefixed != b.Resource.Prefixed:
			return b.Resource.Prefixed
		case a.Principal != b.Principal:
			return a.Principal < b.Principal
		case a.Operation != b.Operation:
			return a.Operation < b.Operation
		case a.Deny != b.Deny:
			return b.Deny
		}
		return a.Host < b.Host
	})
}
