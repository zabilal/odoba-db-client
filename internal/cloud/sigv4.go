package cloud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// AWS Signature Version 4, as AWS documents it in "Create a signed AWS API
// request". It is written out here rather than taken from an SDK for the
// reason ADR-0110 gives, and it is held to AWS's own published test vector
// rather than to this implementation's opinion of itself.
const (
	algorithm   = "AWS4-HMAC-SHA256"
	termination = "aws4_request"

	// longDate and shortDate are the two formats a signature needs: the
	// instant it was made, and the day whose key signed it.
	longDate  = "20060102T150405Z"
	shortDate = "20060102"
)

// uriEncode encodes every byte except the unreserved ones, which is what AWS
// asks for and is not what Go's own query encoder does: that one writes a
// space as "+" and AWS requires "%20". A signature computed over the wrong
// encoding is rejected with no explanation of which byte was at fault, so the
// encoder is written out here rather than borrowed.
//
// The forward slash is encoded too. Nothing signed here is an S3 object key,
// which is the one place AWS exempts it.
func uriEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			const hexDigits = "0123456789ABCDEF"
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xf])
		}
	}
	return b.String()
}

// pair is one query parameter or one header, kept in order rather than in a
// map because canonical order is part of what is signed.
type pair struct{ name, value string }

// canonicalQuery sorts and encodes the query parameters. The sort is on the
// encoded name, because AWS sorts after encoding and the two orders differ
// wherever a name contains a character that encoding moves.
func canonicalQuery(params []pair) string {
	encoded := make([]string, 0, len(params))
	for _, p := range params {
		encoded = append(encoded, uriEncode(p.name)+"="+uriEncode(p.value))
	}
	sort.Strings(encoded)
	return strings.Join(encoded, "&")
}

// canonicalRequest arranges a request the one way AWS will also arrange it
// when it checks the signature, and says which headers were signed.
func canonicalRequest(method, path, query string, headers []pair, payloadHash string) (canonical, signed string) {
	lines := make([]string, 0, len(headers))
	names := make([]string, 0, len(headers))
	sorted := append([]pair(nil), headers...)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].name) < strings.ToLower(sorted[j].name)
	})
	for _, h := range sorted {
		name := strings.ToLower(h.name)
		lines = append(lines, name+":"+strings.TrimSpace(h.value))
		names = append(names, name)
	}
	signed = strings.Join(names, ";")
	if path == "" {
		path = "/"
	}
	canonical = strings.Join([]string{
		method,
		path,
		query,
		strings.Join(lines, "\n") + "\n",
		signed,
		payloadHash,
	}, "\n")
	return canonical, signed
}

// credentialScope is what a signature is confined to: one day, one region,
// one service. It is why a signature stolen from one cannot be spent on
// another.
func credentialScope(t time.Time, region, service string) string {
	return strings.Join([]string{t.UTC().Format(shortDate), region, service, termination}, "/")
}

// stringToSign is the canonical request, hashed, under the scope it belongs
// to. It deliberately does not end in a newline.
func stringToSign(t time.Time, scope, canonical string) string {
	return strings.Join([]string{
		algorithm,
		t.UTC().Format(longDate),
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")
}

// signingKey is the secret carried through the scope one step at a time, so
// that the key which signs is specific to a day, a region and a service and
// the secret itself never is.
func signingKey(secret string, t time.Time, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), t.UTC().Format(shortDate))
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, termination)
}

func signature(key []byte, toSign string) string {
	return hex.EncodeToString(hmacSHA256(key, toSign))
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
