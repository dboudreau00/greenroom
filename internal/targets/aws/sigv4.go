// Package aws is a dependency-free AWS client covering what Greenroom needs:
// the standard credential chain (environment, shared files with assume-role,
// web identity for EKS pods, ECS task roles, EC2 instance metadata),
// Signature Version 4 signing, the EC2 query API and the SSM JSON API.
package aws

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials is an access key pair with an optional session token.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
	Source          string
}

// Valid reports whether the credentials are usable now.
func (c Credentials) Valid() bool {
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return false
	}
	return c.Expiration.IsZero() || time.Now().Add(2*time.Minute).Before(c.Expiration)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Sign adds SigV4 headers to req for service in region. payload is the
// request body (may be nil). now is the signing time.
func Sign(req *http.Request, creds Credentials, region, service string, payload []byte, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	req.Header.Set("Host", host)
	payloadHash := sha256Hex(payload)

	// Canonical headers: lower-case names, trimmed values, sorted.
	var names []string
	canon := map[string]string{}
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "user-agent" || lk == "content-length" {
			continue
		}
		names = append(names, lk)
		var parts []string
		for _, v := range vs {
			parts = append(parts, strings.Join(strings.Fields(v), " "))
		}
		canon[lk] = strings.Join(parts, ",")
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n)
		ch.WriteString(":")
		ch.WriteString(canon[n])
		ch.WriteString("\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := canonicalQueryString(req.URL.Query())

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		ch.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{date, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+creds.SecretAccessKey), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	sig := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+creds.AccessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+sig)
}

// canonicalQueryString encodes query parameters the way SigV4 expects:
// sorted by key then value, RFC 3986 escaped.
func canonicalQueryString(q url.Values) string {
	type kv struct{ k, v string }
	var pairs []kv
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, kv{awsEscape(k), awsEscape(v)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	var sb strings.Builder
	for i, p := range pairs {
		if i > 0 {
			sb.WriteString("&")
		}
		sb.WriteString(p.k)
		sb.WriteString("=")
		sb.WriteString(p.v)
	}
	return sb.String()
}

// awsEscape percent-encodes everything except unreserved characters.
func awsEscape(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			sb.WriteByte(c)
			continue
		}
		sb.WriteByte('%')
		sb.WriteByte(hexDigits[c>>4])
		sb.WriteByte(hexDigits[c&15])
	}
	return sb.String()
}

// FormEncode builds a query-protocol body with AWS escaping.
func FormEncode(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString("&")
		}
		sb.WriteString(awsEscape(k))
		sb.WriteString("=")
		sb.WriteString(awsEscape(params[k]))
	}
	return sb.String()
}
