package aws

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Provider resolves credentials through the standard chain and caches them.
type Provider struct {
	profile string
	region  string
	http    *http.Client
	mu      sync.Mutex
	cached  Credentials
	// Endpoints are overridable for tests.
	IMDSEndpoint      string
	ContainerEndpoint string
	STSEndpoint       string
}

// NewProvider returns a provider for a profile (empty means AWS_PROFILE or
// default).
func NewProvider(profile, region string, client *http.Client) *Provider {
	if profile == "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Provider{profile: profile, region: region, http: client,
		IMDSEndpoint: "http://169.254.169.254", ContainerEndpoint: "http://169.254.170.2"}
}

// Get returns valid credentials, refreshing when needed.
func (p *Provider) Get(ctx context.Context) (Credentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached.Valid() {
		return p.cached, nil
	}
	var errs []error
	for _, src := range []func(context.Context) (Credentials, error){p.fromEnv, p.fromWebIdentity, p.fromSharedFiles, p.fromContainer, p.fromIMDS} {
		c, err := src(ctx)
		if err != nil {
			if !errors.Is(err, errSkip) {
				errs = append(errs, err)
			}
			continue
		}
		p.cached = c
		return c, nil
	}
	if len(errs) == 0 {
		return Credentials{}, errors.New("no AWS credentials found (set AWS_ACCESS_KEY_ID, AWS_PROFILE, a web identity, or run on AWS)")
	}
	return Credentials{}, fmt.Errorf("no AWS credentials found: %w", errors.Join(errs...))
}

var errSkip = errors.New("skip")

func (p *Provider) fromEnv(context.Context) (Credentials, error) {
	id, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if id == "" || secret == "" {
		return Credentials{}, errSkip
	}
	return Credentials{AccessKeyID: id, SecretAccessKey: secret, SessionToken: os.Getenv("AWS_SESSION_TOKEN"), Source: "environment"}, nil
}

func (p *Provider) fromWebIdentity(ctx context.Context) (Credentials, error) {
	tokenFile, roleArn := os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE"), os.Getenv("AWS_ROLE_ARN")
	if tokenFile == "" || roleArn == "" {
		return Credentials{}, errSkip
	}
	tok, err := os.ReadFile(tokenFile)
	if err != nil {
		return Credentials{}, fmt.Errorf("web identity token: %w", err)
	}
	session := os.Getenv("AWS_ROLE_SESSION_NAME")
	if session == "" {
		session = "greenroom"
	}
	c, err := p.assumeRoleWithWebIdentity(ctx, roleArn, session, strings.TrimSpace(string(tok)))
	if err != nil {
		return Credentials{}, err
	}
	c.Source = "web identity " + roleArn
	return c, nil
}

// SharedProfile is what the ~/.aws files say about a profile.
type SharedProfile struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
	RoleArn         string
	SourceProfile   string
	ExternalID      string
	RoleSessionName string
}

// LoadSharedProfile reads a profile from the credentials and config files.
func LoadSharedProfile(name string) (SharedProfile, bool) {
	home, _ := os.UserHomeDir()
	credPath := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	if credPath == "" {
		credPath = filepath.Join(home, ".aws", "credentials")
	}
	cfgPath := os.Getenv("AWS_CONFIG_FILE")
	if cfgPath == "" {
		cfgPath = filepath.Join(home, ".aws", "config")
	}
	var sp SharedProfile
	found := false
	if sec, ok := readINI(credPath)[name]; ok {
		found = true
		sp.AccessKeyID = sec["aws_access_key_id"]
		sp.SecretAccessKey = sec["aws_secret_access_key"]
		sp.SessionToken = sec["aws_session_token"]
	}
	cfg := readINI(cfgPath)
	key := "profile " + name
	if name == "default" {
		key = "default"
	}
	if sec, ok := cfg[key]; ok {
		found = true
		sp.Region = sec["region"]
		sp.RoleArn = sec["role_arn"]
		sp.SourceProfile = sec["source_profile"]
		sp.ExternalID = sec["external_id"]
		sp.RoleSessionName = sec["role_session_name"]
		if sp.AccessKeyID == "" {
			sp.AccessKeyID = sec["aws_access_key_id"]
			sp.SecretAccessKey = sec["aws_secret_access_key"]
			sp.SessionToken = sec["aws_session_token"]
		}
	}
	return sp, found
}

func (p *Provider) fromSharedFiles(ctx context.Context) (Credentials, error) {
	sp, ok := LoadSharedProfile(p.profile)
	if !ok {
		return Credentials{}, errSkip
	}
	if sp.RoleArn != "" {
		src := sp.SourceProfile
		if src == "" {
			src = "default"
		}
		base, ok := LoadSharedProfile(src)
		if !ok || base.AccessKeyID == "" {
			return Credentials{}, fmt.Errorf("profile %s: source_profile %q has no keys", p.profile, src)
		}
		baseCreds := Credentials{AccessKeyID: base.AccessKeyID, SecretAccessKey: base.SecretAccessKey, SessionToken: base.SessionToken}
		session := sp.RoleSessionName
		if session == "" {
			session = "greenroom"
		}
		c, err := p.assumeRole(ctx, baseCreds, sp.RoleArn, session, sp.ExternalID)
		if err != nil {
			return Credentials{}, fmt.Errorf("profile %s: %w", p.profile, err)
		}
		c.Source = "profile " + p.profile + " (assumed " + sp.RoleArn + ")"
		return c, nil
	}
	if sp.AccessKeyID == "" {
		return Credentials{}, errSkip
	}
	return Credentials{AccessKeyID: sp.AccessKeyID, SecretAccessKey: sp.SecretAccessKey, SessionToken: sp.SessionToken, Source: "profile " + p.profile}, nil
}

func (p *Provider) fromContainer(ctx context.Context) (Credentials, error) {
	rel, full := os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"), os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI")
	if rel == "" && full == "" {
		return Credentials{}, errSkip
	}
	u := full
	if u == "" {
		u = p.ContainerEndpoint + rel
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if tok := os.Getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"); tok != "" {
		req.Header.Set("Authorization", tok)
	}
	c, err := p.readCredsJSON(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("container credentials: %w", err)
	}
	c.Source = "container role"
	return c, nil
}

func (p *Provider) fromIMDS(ctx context.Context) (Credentials, error) {
	if os.Getenv("AWS_EC2_METADATA_DISABLED") == "true" {
		return Credentials{}, errSkip
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, p.IMDSEndpoint+"/latest/api/token", nil)
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	resp, err := p.http.Do(req)
	if err != nil {
		return Credentials{}, errSkip
	}
	tokB, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return Credentials{}, errSkip
	}
	token := string(tokB)
	get := func(path string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.IMDSEndpoint+path, nil)
		req.Header.Set("X-aws-ec2-metadata-token", token)
		resp, err := p.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("imds %s: %s", path, resp.Status)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	}
	role, err := get("/latest/meta-data/iam/security-credentials/")
	if err != nil {
		return Credentials{}, fmt.Errorf("instance role: %w", err)
	}
	name := strings.TrimSpace(strings.SplitN(string(role), "\n", 2)[0])
	if name == "" {
		return Credentials{}, errors.New("instance has no IAM role")
	}
	b, err := get("/latest/meta-data/iam/security-credentials/" + name)
	if err != nil {
		return Credentials{}, err
	}
	c, err := parseCredsJSON(b)
	if err != nil {
		return Credentials{}, err
	}
	c.Source = "instance role " + name
	return c, nil
}

func (p *Provider) readCredsJSON(req *http.Request) (Credentials, error) {
	resp, err := p.http.Do(req)
	if err != nil {
		return Credentials{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != 200 {
		return Credentials{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return parseCredsJSON(b)
}

func parseCredsJSON(b []byte) (Credentials, error) {
	var v struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		Token           string `json:"Token"`
		Expiration      string `json:"Expiration"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return Credentials{}, err
	}
	c := Credentials{AccessKeyID: v.AccessKeyID, SecretAccessKey: v.SecretAccessKey, SessionToken: v.Token}
	if v.Expiration != "" {
		c.Expiration, _ = time.Parse(time.RFC3339, v.Expiration)
	}
	if !c.Valid() {
		return Credentials{}, errors.New("credential document had no usable keys")
	}
	return c, nil
}

func (p *Provider) stsEndpoint() string {
	if p.STSEndpoint != "" {
		return p.STSEndpoint
	}
	region := p.region
	if region == "" {
		region = "us-east-1"
	}
	return "https://sts." + region + ".amazonaws.com/"
}

type stsCredentials struct {
	AccessKeyID     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey"`
	SessionToken    string `xml:"SessionToken"`
	Expiration      string `xml:"Expiration"`
}

func (p *Provider) assumeRoleWithWebIdentity(ctx context.Context, roleArn, session, token string) (Credentials, error) {
	body := FormEncode(map[string]string{
		"Action": "AssumeRoleWithWebIdentity", "Version": "2011-06-15",
		"RoleArn": roleArn, "RoleSessionName": session, "WebIdentityToken": token, "DurationSeconds": "3600",
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.stsEndpoint(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	var out struct {
		Result struct {
			Credentials stsCredentials `xml:"Credentials"`
		} `xml:"AssumeRoleWithWebIdentityResult"`
	}
	if err := p.stsCall(req, &out); err != nil {
		return Credentials{}, fmt.Errorf("AssumeRoleWithWebIdentity: %w", err)
	}
	return fromSTS(out.Result.Credentials)
}

func (p *Provider) assumeRole(ctx context.Context, base Credentials, roleArn, session, externalID string) (Credentials, error) {
	params := map[string]string{"Action": "AssumeRole", "Version": "2011-06-15", "RoleArn": roleArn, "RoleSessionName": session, "DurationSeconds": "3600"}
	if externalID != "" {
		params["ExternalId"] = externalID
	}
	body := FormEncode(params)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.stsEndpoint(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	region := p.region
	if region == "" {
		region = "us-east-1"
	}
	Sign(req, base, region, "sts", []byte(body), time.Now())
	var out struct {
		Result struct {
			Credentials stsCredentials `xml:"Credentials"`
		} `xml:"AssumeRoleResult"`
	}
	if err := p.stsCall(req, &out); err != nil {
		return Credentials{}, fmt.Errorf("AssumeRole: %w", err)
	}
	return fromSTS(out.Result.Credentials)
}

func (p *Provider) stsCall(req *http.Request, out any) error {
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode != 200 {
		return parseXMLError(resp.StatusCode, b)
	}
	return xml.Unmarshal(b, out)
}

func fromSTS(c stsCredentials) (Credentials, error) {
	out := Credentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken}
	out.Expiration, _ = time.Parse(time.RFC3339, c.Expiration)
	if !out.Valid() {
		return Credentials{}, errors.New("STS returned no credentials")
	}
	return out, nil
}

// readINI parses the simple INI dialect used by the AWS shared files.
func readINI(path string) map[string]map[string]string {
	out := map[string]map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var cur map[string]string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			cur = map[string]string{}
			out[name] = cur
			continue
		}
		if cur == nil {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cur[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out
}

// ResolveRegion picks the region from the option, the environment, or the
// shared config profile.
func ResolveRegion(region, profile string) string {
	if region != "" {
		return region
	}
	if v := os.Getenv("AWS_REGION"); v != "" {
		return v
	}
	if v := os.Getenv("AWS_DEFAULT_REGION"); v != "" {
		return v
	}
	if profile == "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}
	if sp, ok := LoadSharedProfile(profile); ok && sp.Region != "" {
		return sp.Region
	}
	return ""
}
