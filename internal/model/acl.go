package model

import "strings"

// A cluster's permissions (FR-13.15).
//
// Kafka calls them ACLs, and each one is a sentence: this principal may — or
// may not — do this thing to this resource, from this host. There is no
// hierarchy and no inheritance, and the only precedence is that a deny beats
// an allow wherever both match. So the list of them is the whole truth about
// who can do what, which is why reading it is worth as much as changing it.
//
// The words here are the ones a person says rather than the ones the wire
// carries: a driver maps them to its own protocol's spellings, which is where
// "transactional id" becomes TRANSACTIONAL_ID.

// ACLResourceKind is the sort of thing a permission is about.
type ACLResourceKind string

const (
	ACLTopic           ACLResourceKind = "topic"
	ACLGroup           ACLResourceKind = "group"
	ACLClusterItself   ACLResourceKind = "cluster"
	ACLTransactionalID ACLResourceKind = "transactional id"
	ACLDelegationToken ACLResourceKind = "delegation token"
	ACLUserPrincipal   ACLResourceKind = "user"
)

// ACLResourceKinds are the kinds a permission can be written about here, in
// the order a window offers them: the two that nearly every permission is
// about, then the rest.
//
// ACLUserPrincipal is not among them. A permission about a user — who may mint
// a delegation token for whom — is shown where a cluster holds one, because a
// permission nobody can see is worse than one that cannot be written; but
// nothing here offers to write one, and a choice that failed when it was taken
// would be worse than a choice that is not there.
var ACLResourceKinds = []ACLResourceKind{ACLTopic, ACLGroup, ACLClusterItself,
	ACLTransactionalID, ACLDelegationToken}

// ACLOperation is what a permission allows or denies.
type ACLOperation string

const (
	ACLRead            ACLOperation = "read"
	ACLWrite           ACLOperation = "write"
	ACLCreate          ACLOperation = "create"
	ACLDelete          ACLOperation = "delete"
	ACLAlter           ACLOperation = "alter"
	ACLDescribe        ACLOperation = "describe"
	ACLAlterConfigs    ACLOperation = "alter configs"
	ACLDescribeConfigs ACLOperation = "describe configs"
	ACLClusterAction   ACLOperation = "cluster action"
	ACLIdempotentWrite ACLOperation = "idempotent write"
	ACLAll             ACLOperation = "everything"
)

// ACLOperations are the operations a permission can be about, in the order a
// window offers them: reading and writing first, because that is what nearly
// every permission a person grants is for.
var ACLOperations = []ACLOperation{ACLRead, ACLWrite, ACLDescribe, ACLCreate,
	ACLDelete, ACLAlter, ACLDescribeConfigs, ACLAlterConfigs, ACLIdempotentWrite,
	ACLClusterAction, ACLAll}

// ACLResource is what a permission is about.
type ACLResource struct {
	Kind ACLResourceKind

	// Name is the object's name. Empty means every object of the kind — which
	// the wire writes as "*" — and the cluster itself has no name to give.
	Name string

	// Prefixed says the name is the beginning of a name rather than the whole
	// of one: "orders." prefixed covers orders.eu and orders.us.
	Prefixed bool
}

// ACL is one permission.
type ACL struct {
	// Principal is who it is about, as the cluster writes it: "User:alice".
	Principal string

	// Host is where they may do it from, and "*" is anywhere. A cluster that
	// has never been told otherwise says "*" for every permission it holds.
	Host string

	Resource  ACLResource
	Operation ACLOperation

	// Deny inverts it. A deny beats an allow wherever both match, so which of
	// the two a permission is matters more than anything else about it — which
	// is why it is a field and not a footnote.
	Deny bool
}

// ACLFilter narrows a listing. An empty field means "any", so the zero filter
// asks for every permission a cluster holds.
type ACLFilter struct {
	// Kind and Name are the resource asked about. A named resource matches the
	// permissions written about that name, about every name of the kind, and
	// about any prefix of it: all three apply to it, so all three are what
	// somebody asking about one topic is asking for.
	Kind ACLResourceKind
	Name string

	// Principal narrows it to one principal.
	Principal string
}

// Anything reports whether the filter asks for everything a cluster holds.
func (f ACLFilter) Anything() bool { return f == ACLFilter{} }

// About is the resource a permission is about, in words: "the topic orders",
// "any topic", "topics beginning with orders.", "the cluster".
func (r ACLResource) About() string {
	kind := string(r.Kind)
	switch {
	case r.Kind == ACLClusterItself:
		return "the cluster"
	case r.Name == "":
		return "any " + kind
	case r.Prefixed:
		return kind + "s beginning with " + r.Name
	}
	return "the " + kind + " " + r.Name
}

// String is the permission as a sentence, for a person about to grant or
// revoke it: "User:alice may read the topic orders, from anywhere".
func (a ACL) String() string {
	may := "may"
	if a.Deny {
		may = "may not"
	}
	var b strings.Builder
	b.WriteString(a.Principal)
	b.WriteString(" ")
	b.WriteString(may)
	b.WriteString(" ")
	if a.Operation == ACLAll {
		b.WriteString("do anything to")
	} else {
		b.WriteString(string(a.Operation))
	}
	b.WriteString(" ")
	b.WriteString(a.Resource.About())
	b.WriteString(", from ")
	if a.Host == "" || a.Host == "*" {
		b.WriteString("anywhere")
	} else {
		b.WriteString(a.Host)
	}
	return b.String()
}
