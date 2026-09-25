// Package dynamodb is the Amazon DynamoDB driver (T4.6), over aws-sdk-go-v2.
//
// DynamoDB is a document store that calls its collections tables. An item has
// a primary key — a partition key, optionally with a sort key — and beyond
// those two attributes nothing about its shape is declared: two items in the
// same table need have nothing else in common. So the tree shows collections
// and the grid's columns come from sampling, as MongoDB's do, and the model's
// word for a set of documents is the word used here (ADR-0157).
//
// The SDK is used rather than the protocol written out, which is the opposite
// of the choice ADR-0110 made for cloud token minting. That was three SDKs
// for one small documented signature; this is a data client, where the
// AttributeValue types, the paging and the retries are the thing itself, and
// writing them again would be risking somebody's data to save 3.9 MB.
package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const driverID = "dynamodb"

func init() { source.Register(Driver{}) }

// Driver connects to DynamoDB.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "Amazon DynamoDB", Paradigm: model.ParadigmDocument,
		URLSchemes: []string{"dynamodb"},
		Fields: []source.Field{
			{Key: "region", Label: "Region", Kind: source.FieldText, Required: true, Default: "us-east-1",
				Help: "The AWS region the tables are in, such as eu-west-2."},
			{Key: "endpoint", Label: "Endpoint", Kind: source.FieldText,
				Help: "Optional: a DynamoDB of your own, such as http://localhost:8000 for DynamoDB Local. " +
					"Left empty, the region's own endpoint."},
			{Key: "user", Label: "Access key ID", Kind: source.FieldText,
				Help: "Left empty, the identity this machine already holds is used: a profile, " +
					"an environment variable, or an instance role."},
			{Key: "password", Label: "Secret access key", Kind: source.FieldPassword, Secret: true},
			{Key: "token", Label: "Session token", Kind: source.FieldPassword, Secret: true,
				Help: "Only for temporary credentials."},
		},
	}
}

// Open builds a client and proves it can reach the service, and nothing more
// (source.Driver: a test connection must be fast and precise).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	region := strings.TrimSpace(cfg.Params["region"])
	if region == "" {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Name the region the tables are in."}
	}
	// Retrying is the SDK's, and its default is sensible; what is not
	// sensible for a person waiting on a connection test is retrying a
	// refused connection three times before saying so.
	loaded, err := awsconfig.LoadDefaultConfig(ctx, loadOptions(cfg, region)...)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "Those credentials could not be read.", Err: err}
	}
	endpoint := strings.TrimSpace(cfg.Params["endpoint"])
	client := dynamodb.NewFromConfig(loaded, func(o *dynamodb.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.RetryMaxAttempts = retryAttempts
	})
	s := &dynamoSource{client: client, cfg: cfg, region: region, endpoint: endpoint,
		keys: map[string]model.RowIdentity{}}
	// The smallest thing there is to read, and the one call every DynamoDB
	// identity may make: it names what is there without reading any of it.
	if _, err := client.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)}); err != nil {
		return nil, classifyConnectError(err)
	}
	return s, nil
}

// retryAttempts is how many times a call is tried. The SDK's own default is
// three, which for a connection test means waiting through two retries of a
// refusal that will not change; two is one retry, which covers a throttle
// without making a wrong address slow to find out about.
const retryAttempts = 2

// loadOptions is how the SDK's configuration is built: the region, and the
// credentials somebody typed where they typed any.
//
// It is a function of its own so that what reaches the SDK can be checked
// without a service to connect to — which is the only way to check it, a
// connection that worked proving nothing about which identity it used.
func loadOptions(cfg source.ConnectionConfig, region string) []func(*awsconfig.LoadOptions) error {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if creds, ok := static(cfg); ok {
		opts = append(opts, awsconfig.WithCredentialsProvider(creds))
	}
	return opts
}

// static is the credentials somebody typed, where they typed any. Where they
// did not, the SDK's own chain is used — a profile, the environment, an
// instance role — which is the ambient identity FR-1.14 is about, and is how
// most people reach DynamoDB.
func static(cfg source.ConnectionConfig) (aws.CredentialsProvider, bool) {
	id := strings.TrimSpace(cfg.User)
	if id == "" {
		return nil, false
	}
	secret, token := "", ""
	if cfg.Secret != nil {
		secret, _ = cfg.Secret("password")
		token, _ = cfg.Secret("token")
	}
	return credentials.NewStaticCredentialsProvider(id, secret, token), true
}

// dynamoSource is one region, through one identity.
type dynamoSource struct {
	client   *dynamodb.Client
	cfg      source.ConnectionConfig
	region   string
	endpoint string

	mu sync.Mutex
	// keys remembers each table's primary key, which is what tells its items
	// apart and is asked for on every browse and every write.
	keys map[string]model.RowIdentity
}

var (
	_ source.Source        = (*dynamoSource)(nil)
	_ source.Countable     = (*dynamoSource)(nil)
	_ source.ShapeInferrer = (*dynamoSource)(nil)
	_ source.Writer        = (*dynamoSource)(nil)
)

func (s *dynamoSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmDocument,
		// One connection is one region, and a region is the container: there
		// is nothing above a table to choose between. A table's shape is
		// sampled, not declared, which is what InferredShape says.
		Structure: capability.Structure{InferredShape: true},
		// No query language is claimed. DynamoDB has PartiQL, and a driver
		// that spoke it would be a larger thing than the requirements ask of
		// this source; what is here is the paradigm-neutral path, which is
		// what every source must have and what this one has all of.
		Query: capability.Query{},
		Data: capability.Data{
			// The service filters. It does not sort: a scan returns items in
			// the order it finds them, and there is no ORDER BY to ask for —
			// only a query on a sort key, which is a different operation from
			// reading a table (ADR-0157).
			ServerFilter: true, ServerSort: false,
			// Counting is a scan that returns no items, which is affordable
			// and exact; the estimate the service keeps for a table is up to
			// six hours old and is what the badge shows, said to be one.
			ExactCount: true, ApproximateCount: true,
			Insert: true, Update: true, Delete: true,
			// Items are written one at a time. TransactWriteItems exists and
			// takes at most a hundred items, so a changeset of more than that
			// could not be atomic — promising atomicity for some changesets
			// and not others is worse than promising none (FR-4.5).
			TransactionalWrite: false,
			// No bulk load, as for every other store here whose rows are not
			// a table's: importing a file needs the load contract's rules —
			// emptying the target first, a batch a transaction, a row refused
			// stopping the load — and DynamoDB has none of them. PutItem
			// overwrites rather than refusing, BatchWriteItem is not atomic
			// across its twenty-five items, and there is no way to empty a
			// table but to read every key and delete it. Claiming it would be
			// claiming the rules rather than the calls.
			BulkLoad: false,
			// A picklist would mean scanning the whole table and counting in
			// memory, which is a full read of a store charged by the read.
			// The filter still takes a value somebody types.
			DistinctValues: false,
		},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindFolder: true,
			model.KindCollection: true, model.KindIndex: true,
		},
	}
}

// Info says what answered. DynamoDB publishes no version — it is a service,
// not a server somebody runs — so what there is to say is where it is and
// which identity is reading it.
func (s *dynamoSource) Info(ctx context.Context) (source.ServerInfo, error) {
	start := time.Now()
	if _, err := s.client.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)}); err != nil {
		return source.ServerInfo{}, statementError(err, ctx)
	}
	attrs := map[string]string{"region": s.region}
	if s.endpoint != "" {
		// A DynamoDB of one's own is not the service, and a health display
		// that said otherwise would be telling somebody they were looking at
		// production when they were looking at a container.
		attrs["endpoint"] = s.endpoint
	}
	product := "Amazon DynamoDB"
	if s.endpoint != "" {
		product = "DynamoDB (" + s.endpoint + ")"
	}
	return source.ServerInfo{Product: product, Attrs: attrs, Latency: time.Since(start)}, nil
}

func (s *dynamoSource) Ping(ctx context.Context) error {
	_, err := s.client.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)})
	return statementError(err, ctx)
}

// Close has nothing to release: the SDK's client holds an HTTP client whose
// connections the runtime closes when they go idle, and there is no session
// on the service to end.
func (s *dynamoSource) Close() error { return nil }

// classifyConnectError says what went wrong in terms a person can act on
// (FR-1.4).
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	var notFound *ddbtypes.ResourceNotFoundException
	switch {
	case isAuthFailure(err):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "Those credentials were not accepted.", Err: err}
	case isNoCredentials(err):
		return &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "No credentials were found. Give an access key, or set up a profile or an " +
				"instance role for this machine to use.", Err: err}
	case errors.As(err, &notFound):
		return &source.ConnectError{Kind: source.ConnectNoDatabase,
			Hint: "There is nothing at that endpoint in that region.", Err: err}
	case isUnreachable(err):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The endpoint could not be reached.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The connection failed.", Err: err}
}

// The service's own words for a credential it will not take. Read as text
// because the SDK models them as one error type with a code, and the code is
// what the service documents.
func isAuthFailure(err error) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.ErrorCode() {
	case "UnrecognizedClientException", "InvalidSignatureException",
		"AccessDeniedException", "InvalidClientTokenId", "MissingAuthenticationToken",
		"ExpiredTokenException", "InvalidSecurityToken":
		return true
	}
	return false
}

// isNoCredentials reports a failure to find an identity at all, which is a
// fault in the settings rather than a refusal by the service. The SDK says it
// in prose rather than as a type, so this is the prose it says.
//
// It is asked before unreachability, because looking for an instance role off
// that cloud times out, and "the metadata service did not answer" is not what
// somebody needs to read: what they need to read is that there is no identity.
func isNoCredentials(err error) bool {
	msg := err.Error()
	for _, s := range []string{"failed to refresh cached credentials",
		"no EC2 IMDS role found", "failed to retrieve credentials"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// isUnreachable reports a failure to get a request to the service at all. Also
// read as prose: the SDK wraps a network failure in its own error and the
// cause is several layers down, and the words are the same words every Go
// network failure uses.
func isUnreachable(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"connection refused", "no such host", "timeout",
		"deadline exceeded", "dial tcp", "eof", "connection reset"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// statementError carries a service failure as the contract's StatementError.
// A call stopped by its context reports the cancellation, not whatever the
// SDK said about giving up.
func statementError(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	msg := source.Message{Level: source.MessageError, Text: err.Error()}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		msg.Code = ae.ErrorCode()
		if m := ae.ErrorMessage(); m != "" {
			msg.Text = m
		} else {
			// Several of the service's own exceptions carry no message at
			// all — a failed condition is the commonest — and an error with
			// only a code in it reads as nothing having gone wrong.
			msg.Text = ae.ErrorCode()
		}
	}
	return &source.StatementError{Err: err, Message: msg}
}

// tableOf is the table a ref names.
func tableOf(ref model.ObjectRef) (string, error) {
	if ref.Kind != model.KindCollection || ref.Name() == "" {
		return "", fmt.Errorf("dynamodb: %s holds no items", ref)
	}
	return ref.Name(), nil
}
