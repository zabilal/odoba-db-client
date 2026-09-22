package cloud

import (
	"strings"
	"testing"
	"time"
)

// AWS publishes a Signature Version 4 test suite: a set of requests with the
// canonical request, the string to sign and the signature each one must
// produce. "get-vanilla" is the simplest of them, and the values below are
// that case's published files — not this implementation's output recorded and
// called a test.
//
// This matters more here than a golden file usually does. Nothing in this
// project can reach real AWS, so a signature this code likes is worth
// nothing on its own: it would be this implementation agreeing with itself.
// A digest published by the party that will later check the signature is the
// only evidence available that the arithmetic is the arithmetic AWS does.
const (
	vectorAccessKey = "AKIDEXAMPLE"
	vectorSecretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	vectorRegion    = "us-east-1"
	vectorService   = "service"
	vectorHost      = "example.amazonaws.com"
	vectorStamp     = "20150830T123600Z"

	// From get-vanilla.sts, its fourth line.
	vectorCanonicalHash = "bb579772317eb040ac9ed261061d46c1f17a8133879d6129b6e1c25292927e63"
	// From get-vanilla.authz, the Signature= field.
	vectorSignature = "5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
)

func vectorTime(t *testing.T) time.Time {
	t.Helper()
	at, err := time.Parse(longDate, vectorStamp)
	if err != nil {
		t.Fatalf("the vector's own timestamp will not parse: %v", err)
	}
	return at
}

func TestTheSignatureIsTheOneAWSPublished(t *testing.T) {
	at := vectorTime(t)
	canonical, signed := canonicalRequest("GET", "/", "", []pair{
		{"Host", vectorHost},
		{"X-Amz-Date", vectorStamp},
	}, sha256Hex(nil))

	if want := "host;x-amz-date"; signed != want {
		t.Errorf("signed headers are %q, want %q", signed, want)
	}
	if got := sha256Hex([]byte(canonical)); got != vectorCanonicalHash {
		t.Errorf("the canonical request hashes to\n\t%s\nand AWS publishes\n\t%s\nfor\n%q",
			got, vectorCanonicalHash, canonical)
	}

	scope := credentialScope(at, vectorRegion, vectorService)
	toSign := stringToSign(at, scope, canonical)
	got := signature(signingKey(vectorSecretKey, at, vectorRegion, vectorService), toSign)
	if got != vectorSignature {
		t.Errorf("the signature is\n\t%s\nand AWS publishes\n\t%s\nfor string to sign\n%q",
			got, vectorSignature, toSign)
	}
}

func TestTheStringToSignIsTheOneAWSPublished(t *testing.T) {
	at := vectorTime(t)
	scope := credentialScope(at, vectorRegion, vectorService)
	if want := "20150830/us-east-1/service/aws4_request"; scope != want {
		t.Errorf("the credential scope is %q, want %q", scope, want)
	}
	// get-vanilla.sts, verbatim.
	want := "AWS4-HMAC-SHA256\n" +
		"20150830T123600Z\n" +
		"20150830/us-east-1/service/aws4_request\n" +
		vectorCanonicalHash
	canonical, _ := canonicalRequest("GET", "/", "", []pair{
		{"Host", vectorHost},
		{"X-Amz-Date", vectorStamp},
	}, sha256Hex(nil))
	if got := stringToSign(at, scope, canonical); got != want {
		t.Errorf("the string to sign is\n%q\nand AWS publishes\n%q", got, want)
	}
}

func TestWhatIsEncodedAndWhatIsLeftAlone(t *testing.T) {
	// AWS's rules, which are not Go's: a space is %20 and never "+", a tilde
	// is left alone, and a slash is encoded everywhere this signs.
	for _, c := range []struct{ in, want string }{
		{"abcXYZ019", "abcXYZ019"},
		{"-._~", "-._~"},
		{"a b", "a%20b"},
		{"a/b", "a%2Fb"},
		{"a+b", "a%2Bb"},
		{"jane_doe", "jane_doe"},
		{"20150830/us-east-1/rds-db/aws4_request", "20150830%2Fus-east-1%2Frds-db%2Faws4_request"},
		{"é", "%C3%A9"},
	} {
		if got := uriEncode(c.in); got != c.want {
			t.Errorf("uriEncode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQueryParametersAreSortedAfterTheyAreEncoded(t *testing.T) {
	got := canonicalQuery([]pair{
		{"X-Amz-Date", "20150830T123600Z"},
		{"Action", "connect"},
		{"X-Amz-Credential", "AKID/20150830/us-east-1/rds-db/aws4_request"},
	})
	want := "Action=connect&" +
		"X-Amz-Credential=AKID%2F20150830%2Fus-east-1%2Frds-db%2Faws4_request&" +
		"X-Amz-Date=20150830T123600Z"
	if got != want {
		t.Errorf("the canonical query is\n\t%s\nwant\n\t%s", got, want)
	}
}

func TestHeadersAreSignedInTheirOwnOrderNotTheOneGiven(t *testing.T) {
	canonical, signed := canonicalRequest("GET", "/", "", []pair{
		{"X-Amz-Date", vectorStamp},
		{"Host", vectorHost},
	}, sha256Hex(nil))
	if want := "host;x-amz-date"; signed != want {
		t.Errorf("signed headers are %q, want %q", signed, want)
	}
	if !strings.Contains(canonical, "host:"+vectorHost+"\nx-amz-date:") {
		t.Errorf("the canonical headers are not in order:\n%q", canonical)
	}
	// Given in the other order, it must still be the published request.
	if got := sha256Hex([]byte(canonical)); got != vectorCanonicalHash {
		t.Errorf("order changed the request: %s", got)
	}
}
