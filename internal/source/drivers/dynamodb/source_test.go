package dynamodb

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func kindOf(t *testing.T, err error) source.ConnectKind {
	t.Helper()
	var ce *source.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("that was not a connect error: %v", err)
	}
	return ce.Kind
}

func TestTheDescriptorIsWhatTheFormNeeds(t *testing.T) {
	desc := Driver{}.Describe()
	if desc.ID != driverID {
		t.Errorf("it registers as %q", desc.ID)
	}
	if desc.Paradigm != model.ParadigmDocument {
		t.Errorf("it calls itself %v", desc.Paradigm)
	}
	// No default port: DynamoDB is a service reached by region, not a host
	// and port somebody types.
	if desc.DefaultPort != 0 {
		t.Errorf("it claims port %d", desc.DefaultPort)
	}
	fields := map[string]source.Field{}
	for _, f := range desc.Fields {
		fields[f.Key] = f
	}
	for _, key := range []string{"region", "endpoint", "user", "password", "token"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("the form has no %s", key)
		}
	}
	for _, key := range []string{"password", "token"} {
		if !fields[key].Secret {
			t.Errorf("%s is not a secret, so it would be written to settings.json", key)
		}
	}
	for _, key := range []string{"region", "endpoint", "user"} {
		if fields[key].Secret {
			t.Errorf("%s is a secret, so nobody could see what they typed", key)
		}
	}
	if !fields["region"].Required {
		t.Error("the region is optional, and there is nowhere to connect without it")
	}
	// The access key is optional on purpose: left empty, the identity this
	// machine already holds is used, which is how most people reach DynamoDB.
	if fields["user"].Required {
		t.Error("an access key is required, and an instance role needs none")
	}
	if fields["endpoint"].Required {
		t.Error("an endpoint is required, and the region's own is the default")
	}
}

// A region is needed before anything is built: without one the SDK would
// resolve an endpoint for nowhere.
func TestARegionIsNeeded(t *testing.T) {
	for name, params := range map[string]map[string]string{
		"no params at all": nil,
		"an empty region":  {"region": ""},
		"only spaces":      {"region": "   "},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Driver{}.Open(context.Background(), source.ConnectionConfig{Params: params})
			if err == nil {
				t.Fatal("it connected to nowhere")
			}
			if kindOf(t, err) != source.ConnectConfig {
				t.Errorf("it called that %v", kindOf(t, err))
			}
			// The hint as well as the kind: a machine with no credentials
			// configured also refuses with ConnectConfig, so the kind alone
			// would pass whether the region was looked at or not.
			var ce *source.ConnectError
			errors.As(err, &ce)
			if !strings.Contains(ce.Hint, "region") {
				t.Errorf("it says %q, which does not mention the region", ce.Hint)
			}
		})
	}
}

// Credentials somebody typed are used; where they typed none, the SDK's own
// chain is, which is the ambient identity FR-1.14 is about.
func TestWhichCredentialsAreUsed(t *testing.T) {
	asked := map[string]bool{}
	cfg := source.ConnectionConfig{
		User: "AKIAEXAMPLE",
		Secret: func(key string) (string, error) {
			asked[key] = true
			return "secret-" + key, nil
		},
	}
	creds, ok := static(cfg)
	if !ok {
		t.Fatal("an access key was typed and was not used")
	}
	got, err := creds.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "AKIAEXAMPLE" {
		t.Errorf("it would sign as %q", got.AccessKeyID)
	}
	if got.SecretAccessKey != "secret-password" {
		t.Errorf("its secret is %q", got.SecretAccessKey)
	}
	if got.SessionToken != "secret-token" {
		t.Errorf("its token is %q", got.SessionToken)
	}
	if !asked["password"] || !asked["token"] {
		t.Errorf("it asked for %v", asked)
	}

	// No access key typed: nothing is supplied, and the SDK looks for an
	// identity itself.
	if _, ok := static(source.ConnectionConfig{}); ok {
		t.Error("it made up credentials from nothing")
	}
	if _, ok := static(source.ConnectionConfig{User: "   "}); ok {
		t.Error("spaces were taken for an access key")
	}
	// An access key with no way to ask for its secret is still that key: the
	// service will refuse it, which is a clearer answer than this refusing.
	if _, ok := static(source.ConnectionConfig{User: "AKIA"}); !ok {
		t.Error("an access key with no secret was dropped")
	}
}

func TestAFailureToConnectIsCalledWhatItIs(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want source.ConnectKind
	}{
		"an unknown key":   {apiError{code: "UnrecognizedClientException"}, source.ConnectAuth},
		"a bad signature":  {apiError{code: "InvalidSignatureException"}, source.ConnectAuth},
		"no permission":    {apiError{code: "AccessDeniedException"}, source.ConnectAuth},
		"an expired token": {apiError{code: "ExpiredTokenException"}, source.ConnectAuth},
		"nothing there":    {&net.OpError{Err: syscall.ECONNREFUSED}, source.ConnectUnreachable},
		"no such host":     {errors.New(`dial tcp: lookup nope: no such host`), source.ConnectUnreachable},
		"nothing in time":  {errors.New("context deadline exceeded"), source.ConnectUnreachable},
		// Each of the SDK's three ways of saying it, on its own: a message
		// with two of them in would pass whichever one was being read.
		"credentials that will not refresh": {errors.New("operation error: failed to refresh cached credentials"), source.ConnectConfig},
		"no role on this machine":           {errors.New("EC2RoleRequestError: no EC2 IMDS role found"), source.ConnectConfig},
		"nothing to retrieve":               {errors.New("failed to retrieve credentials from the provider"), source.ConnectConfig},
		"no such table":                     {&ddbtypes.ResourceNotFoundException{}, source.ConnectNoDatabase},
		"something else":                    {errors.New("the wheels came off"), source.ConnectUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			got := classifyConnectError(c.err)
			if k := kindOf(t, got); k != c.want {
				t.Errorf("it called that %v, want %v", k, c.want)
			}
			if !errors.Is(got, c.err) {
				t.Error("it dropped what the service said")
			}
		})
	}
	// A refusal this driver already made is not classified again.
	mine := &source.ConnectError{Kind: source.ConnectConfig, Hint: "Name the region."}
	if got := classifyConnectError(mine); got != error(mine) {
		t.Errorf("it rewrote its own refusal as %v", got)
	}
}

// apiError is one of the service's own errors, with the code it documents.
type apiError struct {
	code, message string
}

func (e apiError) Error() string                 { return e.code + ": " + e.message }
func (e apiError) ErrorCode() string             { return e.code }
func (e apiError) ErrorMessage() string          { return e.message }
func (e apiError) ErrorFault() smithy.ErrorFault { return smithy.FaultServer }

// A call that failed says what the service called it, and says something: an
// error with only a code in it reads as nothing having gone wrong, and several
// of the service's exceptions carry no message at all.
// What reaches the SDK: the region always, and a credentials provider only
// where somebody typed one — which is the only way to check it, a connection
// that worked proving nothing about which identity it used.
func TestWhatReachesTheSDK(t *testing.T) {
	typed := source.ConnectionConfig{User: "AKIA", Secret: func(string) (string, error) { return "s", nil }}
	if got := loadOptions(typed, "eu-west-2"); len(got) != 2 {
		t.Errorf("credentials were typed and %d options were built", len(got))
	}
	if got := loadOptions(source.ConnectionConfig{}, "eu-west-2"); len(got) != 1 {
		t.Errorf("no credentials were typed and %d options were built", len(got))
	}
	// And the region is in there: what it resolves is checked by loading it.
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		loadOptions(source.ConnectionConfig{}, "eu-west-2")...)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "eu-west-2" {
		t.Errorf("it would talk to %q", cfg.Region)
	}
}

func TestAFailedCallSaysWhatWentWrong(t *testing.T) {
	var se *source.StatementError
	err := statementError(apiError{code: "ValidationException", message: "the key is wrong"}, nil)
	if !errors.As(err, &se) {
		t.Fatalf("it said %v", err)
	}
	if se.Message.Code != "ValidationException" || se.Message.Text != "the key is wrong" {
		t.Errorf("it reads %+v", se.Message)
	}
	err = statementError(apiError{code: "ConditionalCheckFailedException"}, nil)
	errors.As(err, &se)
	if se.Message.Text != "ConditionalCheckFailedException" {
		t.Errorf("an exception with no message reads %q", se.Message.Text)
	}
	if statementError(nil, nil) != nil {
		t.Error("nothing wrong became an error")
	}
	// A call stopped by its context reports the cancellation, not whatever the
	// SDK said about giving up.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := statementError(apiError{code: "InternalServerError"}, ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call said %v", err)
	}
	if err := statementError(context.DeadlineExceeded, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a call that ran out of time said %v", err)
	}
}

// What an index carries, in words: an index that projects only its keys
// answers a query about anything else by going back to the table, or not at
// all, which is the commonest surprise about a DynamoDB index.
func TestWhatAnIndexCarries(t *testing.T) {
	for name, c := range map[string]struct {
		p    ddbtypes.Projection
		want string
	}{
		"everything": {ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll}, "every attribute"},
		"the keys":   {ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeKeysOnly}, "the keys only"},
		"some of it": {ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeInclude,
			NonKeyAttributes: []string{"a", "b"}}, "the keys and a, b"},
		"something new": {ddbtypes.Projection{ProjectionType: "SOMETHING"}, "SOMETHING"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := projects(&c.p); got != c.want {
				t.Errorf("it says %q, want %q", got, c.want)
			}
		})
	}
}

// A key's attributes come back partition-key-first, which is the order the
// service states and the order a key's values are given in — and getting it
// backwards would address the wrong item.
func TestAKeyIsPartitionKeyFirst(t *testing.T) {
	name := func(s string) *string { return &s }
	for label, schema := range map[string][]ddbtypes.KeySchemaElement{
		"in order": {
			{AttributeName: name("pk"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: name("sk"), KeyType: ddbtypes.KeyTypeRange},
		},
		"the other way round": {
			{AttributeName: name("sk"), KeyType: ddbtypes.KeyTypeRange},
			{AttributeName: name("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
	} {
		t.Run(label, func(t *testing.T) {
			got := keyColumns(schema)
			if len(got) != 2 || got[0] != "pk" || got[1] != "sk" {
				t.Errorf("the key is %v", got)
			}
		})
	}
	only := keyColumns([]ddbtypes.KeySchemaElement{{AttributeName: name("pk"), KeyType: ddbtypes.KeyTypeHash}})
	if len(only) != 1 || only[0] != "pk" {
		t.Errorf("a key of one is %v", only)
	}
	if got := keyColumns(nil); len(got) != 0 {
		t.Errorf("no key at all is %v", got)
	}
}

// An index's key attributes come back partition-key-first too, and an index
// whose keys were lost would be drawn as an index on nothing.
func TestAnIndexsKeysAreInOrder(t *testing.T) {
	name := func(s string) *string { return &s }
	got := indexKeys([]ddbtypes.KeySchemaElement{
		{AttributeName: name("sk"), KeyType: ddbtypes.KeyTypeRange},
		{AttributeName: name("pk"), KeyType: ddbtypes.KeyTypeHash},
	})
	if len(got) != 2 || got[0].Name != "pk" || got[1].Name != "sk" {
		t.Errorf("its keys are %+v", got)
	}
	if len(indexKeys(nil)) != 0 {
		t.Error("an index with no key schema has keys")
	}
}

// Only a collection holds items: a ref of any other kind is refused rather
// than read as a table name.
func TestOnlyACollectionHoldsItems(t *testing.T) {
	if _, err := tableOf(model.NewRef(model.KindCollection, "r", "T")); err != nil {
		t.Errorf("a collection was refused: %v", err)
	}
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindTable, "r", "T"),
		model.NewRef(model.KindIndex, "r", "T", "i"),
		model.NewRef(model.KindCollection),
		{},
	} {
		if got, err := tableOf(ref); err == nil {
			t.Errorf("%v was read as the table %q", ref, got)
		}
	}
}

// What a type of attribute means to the shape, including the one the wire does
// not distinguish: a whole number and a fraction are both N, and a grid wants
// them apart.
func TestWhatATypeMeansToTheShape(t *testing.T) {
	for native, want := range map[string]model.TypeClass{
		"S": model.TypeString, "N": model.TypeDecimal, "INT": model.TypeInteger,
		"B": model.TypeBytes, "BOOL": model.TypeBool,
		"M": model.TypeJSON, "L": model.TypeJSON, "SS": model.TypeJSON,
		"NS": model.TypeJSON, "BS": model.TypeJSON,
		"NULL": model.TypeUnknown, "?": model.TypeUnknown,
	} {
		if got := typeFor(native).Class; got != want {
			t.Errorf("%s reads as %v, want %v", native, got, want)
		}
	}
	if got := typeFor("INT").Native; got != "N" {
		t.Errorf("a whole number calls itself %q, and the service calls it N", got)
	}
}

// A column made from a sampled attribute: one type is that type, several is
// unknown, because no renderer is right for all of them.
func TestTheColumnAnAttributeBecomes(t *testing.T) {
	ref := model.NewRef(model.KindCollection, "r", "T")
	one := columnOf(model.InferredField{Name: "a", Presence: 1,
		Types: []model.ObservedType{{Type: typeFor("S"), Count: 3}}}, ref)
	if one.Type.Class != model.TypeString || one.Type.Native != "S" {
		t.Errorf("one type reads as %+v", one.Type)
	}
	if one.Type.Nullable {
		t.Error("an attribute every item has is nullable")
	}
	several := columnOf(model.InferredField{Name: "a", Presence: 0.5,
		Types: []model.ObservedType{{Type: typeFor("S"), Count: 3}, {Type: typeFor("N"), Count: 1}}}, ref)
	if several.Type.Class != model.TypeUnknown {
		t.Errorf("two types read as %v", several.Type.Class)
	}
	if several.Type.Native != "S or N" {
		t.Errorf("they call themselves %q", several.Type.Native)
	}
	if !several.Type.Nullable {
		t.Error("an attribute half the items have is not nullable")
	}
	none := columnOf(model.InferredField{Name: "a"}, ref)
	if none.Type.Class != model.TypeUnknown || none.Origin.Name() != "T" {
		t.Errorf("an attribute seen with no type at all reads as %+v", none)
	}
}

// The types an attribute was seen with come back most frequent first, so the
// commonest is what a column is drawn as.
func TestTheTypesAnAttributeWasSeenWith(t *testing.T) {
	got := typesOf(map[string]int64{"N": 1, "S": 5, "B": 5})
	if len(got) != 3 {
		t.Fatalf("it found %+v", got)
	}
	if got[0].Count != 5 || got[1].Count != 5 || got[2].Count != 1 {
		t.Errorf("they are ordered %+v", got)
	}
	// Two seen equally often are ordered by name, so that the same sample
	// reads the same way twice.
	if got[0].Type.Native != "B" || got[1].Type.Native != "S" {
		t.Errorf("the pair is ordered %q, %q", got[0].Type.Native, got[1].Type.Native)
	}
	if len(typesOf(nil)) != 0 {
		t.Error("nothing seen produced a type")
	}
}

// A typed condition is refused rather than ignored: there is no language here
// to have written one in (REQ-DRV-3).
func TestATypedConditionIsRefused(t *testing.T) {
	s := &dynamoSource{}
	for _, where := range []string{"attribute_exists(id)", "1 = 1", "id > 3"} {
		if _, err := s.scanFor("T", source.BrowseOptions{Where: where}); err == nil {
			t.Errorf("%q was accepted", where)
		} else if !strings.Contains(err.Error(), "no language") {
			t.Errorf("%q was refused with %q", where, err)
		}
	}
}

// A projection names its attributes through placeholders too, for the same
// reason a filter does.
func TestAProjectionBindsItsNames(t *testing.T) {
	s := &dynamoSource{}
	in, err := s.scanFor("T", source.BrowseOptions{Columns: []string{"name", "size"}})
	if err != nil {
		t.Fatal(err)
	}
	if in.ProjectionExpression == nil || *in.ProjectionExpression != "#c0, #c1" {
		t.Errorf("it projects %v", in.ProjectionExpression)
	}
	if in.ExpressionAttributeNames["#c0"] != "name" || in.ExpressionAttributeNames["#c1"] != "size" {
		t.Errorf("it bound %v", in.ExpressionAttributeNames)
	}
	// Nothing asked for is every attribute, and no expression at all.
	plain, err := s.scanFor("T", source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.ProjectionExpression != nil || plain.FilterExpression != nil {
		t.Errorf("a plain scan carries %v / %v", plain.ProjectionExpression, plain.FilterExpression)
	}
	if len(plain.ExpressionAttributeNames) != 0 || len(plain.ExpressionAttributeValues) != 0 {
		t.Error("a plain scan binds something")
	}
}
