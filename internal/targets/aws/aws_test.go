package aws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/targets"
)

// The get-vanilla case from the AWS Signature Version 4 test suite.
func TestSignGetVanilla(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	creds := Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}
	Sign(req, creds, "us-east-1", "service", nil, time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization\n got %s\nwant %s", got, want)
	}
	if req.Header.Get("X-Amz-Date") != "20150830T123600Z" {
		t.Fatal("x-amz-date")
	}
}

func TestSignSessionToken(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://ec2.us-east-1.amazonaws.com/", strings.NewReader("Action=X"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	Sign(req, Credentials{AccessKeyID: "a", SecretAccessKey: "b", SessionToken: "tok"}, "us-east-1", "ec2", []byte("Action=X"), time.Now())
	auth := req.Header.Get("Authorization")
	if !strings.Contains(auth, "SignedHeaders=content-type;host;x-amz-date;x-amz-security-token") {
		t.Fatalf("signed headers: %s", auth)
	}
	if req.Header.Get("X-Amz-Security-Token") != "tok" {
		t.Fatal("token header")
	}
}

func TestFormEncode(t *testing.T) {
	got := FormEncode(map[string]string{"Action": "DescribeInstances", "InstanceId.1": "i-1", "Filter": "a b*~"})
	if got != "Action=DescribeInstances&Filter=a%20b%2A~&InstanceId.1=i-1" {
		t.Fatalf("encode %s", got)
	}
}

func TestReadINIAndProfiles(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "credentials")
	cfg := filepath.Join(dir, "config")
	os.WriteFile(cred, []byte("[default]\naws_access_key_id = AKID\naws_secret_access_key = SECRET\n\n[demo]\naws_access_key_id=A2\naws_secret_access_key=S2\naws_session_token=T2\n"), 0o600)
	os.WriteFile(cfg, []byte("# comment\n[profile demo]\nregion = eu-west-1\n\n[profile assumed]\nrole_arn = arn:aws:iam::1:role/x\nsource_profile = demo\nregion = us-west-2\n"), 0o600)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", cred)
	t.Setenv("AWS_CONFIG_FILE", cfg)
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	sp, ok := LoadSharedProfile("demo")
	if !ok || sp.AccessKeyID != "A2" || sp.SessionToken != "T2" || sp.Region != "eu-west-1" {
		t.Fatalf("demo profile %+v %v", sp, ok)
	}
	if ResolveRegion("", "demo") != "eu-west-1" || ResolveRegion("ap-south-1", "demo") != "ap-south-1" {
		t.Fatal("region resolution")
	}
	sp, ok = LoadSharedProfile("assumed")
	if !ok || sp.RoleArn == "" || sp.SourceProfile != "demo" {
		t.Fatalf("assumed %+v", sp)
	}
	if _, ok := LoadSharedProfile("missing"); ok {
		t.Fatal("missing profile found")
	}
}

func clearAWSEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_PROFILE", "AWS_EC2_METADATA_DISABLED"} {
		t.Setenv(k, "")
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "nope"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "nope"))
}

func TestCredentialChainEnv(t *testing.T) {
	clearAWSEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AK")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SK")
	p := NewProvider("", "us-east-1", nil)
	c, err := p.Get(context.Background())
	if err != nil || c.AccessKeyID != "AK" || c.Source != "environment" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestCredentialChainIMDS(t *testing.T) {
	clearAWSEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/latest/api/token":
			io.WriteString(w, "imds-token")
		case r.URL.Path == "/latest/meta-data/iam/security-credentials/":
			if r.Header.Get("X-aws-ec2-metadata-token") != "imds-token" {
				w.WriteHeader(401)
				return
			}
			io.WriteString(w, "demo-role\n")
		case r.URL.Path == "/latest/meta-data/iam/security-credentials/demo-role":
			json.NewEncoder(w).Encode(map[string]string{"AccessKeyId": "IK", "SecretAccessKey": "IS", "Token": "IT", "Expiration": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	p := NewProvider("", "us-east-1", srv.Client())
	p.IMDSEndpoint = srv.URL
	c, err := p.Get(context.Background())
	if err != nil || c.AccessKeyID != "IK" || c.SessionToken != "IT" || c.Source != "instance role demo-role" {
		t.Fatalf("%+v %v", c, err)
	}
	c2, _ := p.Get(context.Background())
	if c2.AccessKeyID != "IK" {
		t.Fatal("cache")
	}
}

func TestCredentialChainContainer(t *testing.T) {
	clearAWSEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "secret" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"AccessKeyId": "CK", "SecretAccessKey": "CS", "Token": "CT", "Expiration": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	}))
	defer srv.Close()
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", srv.URL+"/creds")
	t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN", "secret")
	p := NewProvider("", "us-east-1", srv.Client())
	c, err := p.Get(context.Background())
	if err != nil || c.AccessKeyID != "CK" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestAssumeRoleFromProfile(t *testing.T) {
	clearAWSEnv(t)
	dir := t.TempDir()
	cred := filepath.Join(dir, "credentials")
	cfg := filepath.Join(dir, "config")
	os.WriteFile(cred, []byte("[base]\naws_access_key_id = BK\naws_secret_access_key = BS\n"), 0o600)
	os.WriteFile(cfg, []byte("[profile assumed]\nrole_arn = arn:aws:iam::1:role/x\nsource_profile = base\n"), 0o600)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", cred)
	t.Setenv("AWS_CONFIG_FILE", cfg)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Action=AssumeRole") || !strings.Contains(r.Header.Get("Authorization"), "Credential=BK/") {
			t.Errorf("bad sts request: %s %s", body, r.Header.Get("Authorization"))
		}
		io.WriteString(w, `<AssumeRoleResponse><AssumeRoleResult><Credentials><AccessKeyId>RK</AccessKeyId><SecretAccessKey>RS</SecretAccessKey><SessionToken>RT</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>`)
	}))
	defer srv.Close()
	p := NewProvider("assumed", "us-east-1", srv.Client())
	p.STSEndpoint = srv.URL + "/"
	c, err := p.Get(context.Background())
	if err != nil || c.AccessKeyID != "RK" || !strings.Contains(c.Source, "assumed arn:aws:iam::1:role/x") {
		t.Fatalf("%+v %v", c, err)
	}
}

func fakeEC2(t *testing.T) *Client {
	t.Helper()
	clearAWSEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AK")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SK")
	var started atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
			w.WriteHeader(403)
			io.WriteString(w, `<Response><Errors><Error><Code>AuthFailure</Code><Message>bad sig</Message></Error></Errors></Response>`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		params := string(body)
		switch {
		case strings.Contains(params, "Action=DescribeInstances"):
			state := "stopped"
			if started.Load() > 0 {
				state = "running"
			}
			io.WriteString(w, `<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><reservationSet><item><instancesSet><item>
<instanceId>i-123</instanceId><instanceType>t3.micro</instanceType><imageId>ami-1</imageId><instanceState><code>16</code><name>`+state+`</name></instanceState>
<privateIpAddress>10.0.0.5</privateIpAddress><launchTime>2026-09-01T10:00:00.000Z</launchTime><placement><availabilityZone>us-east-1a</availabilityZone></placement>
<tagSet><item><key>Name</key><value>bastion</value></item></tagSet></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`)
		case strings.Contains(params, "Action=DescribeInstanceStatus"):
			io.WriteString(w, `<DescribeInstanceStatusResponse><instanceStatusSet><item><instanceId>i-123</instanceId><systemStatus><status>ok</status></systemStatus><instanceStatus><status>ok</status></instanceStatus></item></instanceStatusSet></DescribeInstanceStatusResponse>`)
		case strings.Contains(params, "Action=StartInstances"):
			started.Add(1)
			io.WriteString(w, `<StartInstancesResponse><instancesSet/></StartInstancesResponse>`)
		case strings.Contains(params, "Action=GetCallerIdentity"):
			io.WriteString(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Account>123</Account><Arn>arn:aws:iam::123:user/demo</Arn></GetCallerIdentityResult></GetCallerIdentityResponse>`)
		default:
			w.WriteHeader(400)
			io.WriteString(w, `<Response><Errors><Error><Code>InvalidAction</Code><Message>nope</Message></Error></Errors></Response>`)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("us-east-1", "")
	if err != nil {
		t.Fatal(err)
	}
	c.http = srv.Client()
	c.Provider = NewProvider("", "us-east-1", srv.Client())
	c.Endpoints["ec2"] = srv.URL + "/"
	c.Endpoints["sts"] = srv.URL + "/"
	return c
}

func TestEC2(t *testing.T) {
	c := fakeEC2(t)
	ctx := context.Background()
	inst, err := c.Instance(ctx, "i-123")
	if err != nil {
		t.Fatal(err)
	}
	if inst.State != "stopped" || inst.Name != "bastion" || inst.PrivateIP != "10.0.0.5" || inst.LaunchTime.Year() != 2026 || inst.SystemStatus != "ok" {
		t.Fatalf("instance %+v", inst)
	}
	if inst.Healthy() {
		t.Fatal("stopped instance reported healthy")
	}
	if err := c.StartInstances(ctx, "i-123"); err != nil {
		t.Fatal(err)
	}
	inst, err = c.WaitInstance(ctx, "i-123", "running", 10*time.Second)
	if err != nil || !inst.Healthy() {
		t.Fatalf("after start %+v %v", inst, err)
	}
	if msg, err := c.Ping(ctx); err != nil || !strings.Contains(msg, "account 123") {
		t.Fatalf("ping %q %v", msg, err)
	}
	if err := c.StopInstances(ctx); err == nil {
		t.Fatal("expected InvalidAction error")
	} else if ae, ok := err.(*APIError); !ok || ae.Code != "InvalidAction" {
		t.Fatalf("error type %v", err)
	}
}

func TestSSMRun(t *testing.T) {
	clearAWSEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AK")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SK")
	var polls atomic.Int32
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.Header.Get("X-Amz-Target") {
		case "AmazonSSM.SendCommand":
			json.Unmarshal(body, &sent)
			json.NewEncoder(w).Encode(map[string]any{"Command": map[string]any{"CommandId": "cmd-1"}})
		case "AmazonSSM.GetCommandInvocation":
			if polls.Add(1) == 1 {
				json.NewEncoder(w).Encode(map[string]any{"Status": "InProgress"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"Status": "Success", "ResponseCode": 0, "StandardOutputContent": "hello\nworld\n", "StandardErrorContent": ""})
		default:
			w.WriteHeader(400)
			io.WriteString(w, `{"__type":"x#UnknownOperation","message":"nope"}`)
		}
	}))
	defer srv.Close()
	c, _ := NewClient("us-east-1", "")
	c.http = srv.Client()
	c.Provider = NewProvider("", "us-east-1", srv.Client())
	c.Endpoints["ssm"] = srv.URL + "/"
	var lines []string
	res, err := c.Run(context.Background(), targets.RunSpec{InstanceID: "i-1", Argv: []string{"echo", "hello"}, Env: map[string]string{"X": "1"}, WorkDir: "/tmp"}, func(stream, line string) { lines = append(lines, line) })
	if err != nil || res.ExitCode != 0 || res.Stdout != "hello\nworld\n" {
		t.Fatalf("res %+v err %v", res, err)
	}
	if len(lines) != 2 || lines[1] != "world" {
		t.Fatalf("sink lines %v", lines)
	}
	params := sent["Parameters"].(map[string]any)
	cmds := params["commands"].([]any)
	script := cmds[0].(string)
	if !strings.Contains(script, "echo hello") || !strings.Contains(script, "export X=1") || !strings.Contains(script, "cd /tmp") || !strings.Contains(script, "GREENROOM_SSM_EOF") {
		t.Fatalf("script:\n%s", script)
	}
	if sent["DocumentName"] != "AWS-RunShellScript" {
		t.Fatal("document")
	}
	if polls.Load() < 2 {
		t.Fatal("did not poll")
	}
}

func TestParseXMLError(t *testing.T) {
	err := parseXMLError(400, []byte(`<Response><Errors><Error><Code>InvalidInstanceID.NotFound</Code><Message>The instance ID 'i-1' does not exist</Message></Error></Errors></Response>`))
	ae := err.(*APIError)
	if ae.Code != "InvalidInstanceID.NotFound" || !strings.Contains(ae.Message, "does not exist") {
		t.Fatalf("%+v", ae)
	}
}
