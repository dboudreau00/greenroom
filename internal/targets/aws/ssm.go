package aws

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/targets"
)

// jsonCall performs an SSM style JSON 1.1 call.
func (c *Client) jsonCall(ctx context.Context, service, target string, in any, out any) error {
	creds, err := c.Provider.Get(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(service), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)
	Sign(req, creds, c.Region, service, body, time.Now())
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
			Msg     string `json:"Message"`
		}
		json.Unmarshal(b, &e)
		msg := e.Message
		if msg == "" {
			msg = e.Msg
		}
		if msg == "" {
			msg = strings.TrimSpace(string(b))
		}
		code := e.Type
		if i := strings.LastIndex(code, "#"); i >= 0 {
			code = code[i+1:]
		}
		return &APIError{Status: resp.StatusCode, Code: code, Message: msg}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// ssmChunk is the largest script we hand SSM in one parameter. The
// documented limit on a single parameter is larger, but staying well under
// keeps the request comfortably inside the overall document size.
const ssmChunk = 30 * 1024

// Run executes a shell script or command on an EC2 instance through SSM
// Run Command (AWS-RunShellScript). Large scripts are staged onto the
// instance in base64 chunks first, then executed.
func (c *Client) Run(ctx context.Context, spec targets.RunSpec, sink targets.LineSink) (targets.Result, error) {
	if spec.InstanceID == "" {
		return targets.Result{}, errors.New("ssm run needs instanceId")
	}
	ctx, cancel := targets.Deadline(ctx, spec, 15*time.Minute)
	defer cancel()
	start := time.Now()
	script := spec.Script
	if len(spec.Argv) > 0 {
		script = targets.ShellJoin(spec.Argv)
		if spec.Stdin != nil {
			b, err := io.ReadAll(spec.Stdin)
			if err != nil {
				return targets.Result{}, err
			}
			script = targets.Heredoc(script, string(b))
		}
	}
	if spec.WorkDir != "" {
		script = "cd " + targets.ShellQuote(spec.WorkDir) + " || exit 1\n" + script
	}
	if len(spec.Env) > 0 {
		var sb strings.Builder
		for k, v := range spec.Env {
			sb.WriteString("export " + k + "=" + targets.ShellQuote(v) + "\n")
		}
		script = sb.String() + script
	}
	shell := targets.ShellFor(spec)
	var commands []string
	if len(script) <= ssmChunk {
		commands = []string{shell + " -s <<'GREENROOM_SSM_EOF'\n" + script + "\nGREENROOM_SSM_EOF"}
	} else {
		// Stage the script in chunks then run it.
		path := fmt.Sprintf("/tmp/greenroom-%d.sh", time.Now().UnixNano())
		enc := base64.StdEncoding.EncodeToString([]byte(script))
		for off := 0; off < len(enc); off += ssmChunk {
			end := off + ssmChunk
			if end > len(enc) {
				end = len(enc)
			}
			cmd := fmt.Sprintf("printf '%%s' '%s' | base64 -d >> %s", enc[off:end], path)
			// Each chunk is its own SSM invocation so no single parameter
			// exceeds the service limit.
			if _, err := c.runCommands(ctx, spec.InstanceID, []string{cmd}, nil, 5*time.Minute); err != nil {
				return targets.Result{}, fmt.Errorf("staging script: %w", err)
			}
		}
		commands = []string{fmt.Sprintf("%s %s; rc=$?; rm -f %s; exit $rc", shell, path, path)}
	}
	res, err := c.runCommands(ctx, spec.InstanceID, commands, sink, 0)
	res.Duration = time.Since(start)
	return res, err
}

func (c *Client) runCommands(ctx context.Context, instanceID string, commands []string, sink targets.LineSink, timeout time.Duration) (targets.Result, error) {
	if timeout == 0 {
		if dl, ok := ctx.Deadline(); ok {
			timeout = time.Until(dl)
		} else {
			timeout = 15 * time.Minute
		}
	}
	secs := int(timeout.Seconds())
	if secs < 30 {
		secs = 30
	}
	var sent struct {
		Command struct {
			CommandID string `json:"CommandId"`
		} `json:"Command"`
	}
	in := map[string]any{
		"DocumentName":   "AWS-RunShellScript",
		"InstanceIds":    []string{instanceID},
		"Parameters":     map[string][]string{"commands": commands, "executionTimeout": {fmt.Sprint(secs)}},
		"TimeoutSeconds": 60,
		"Comment":        "greenroom",
	}
	if err := c.jsonCall(ctx, "ssm", "AmazonSSM.SendCommand", in, &sent); err != nil {
		return targets.Result{}, err
	}
	col := targets.NewCollector(sink, 0)
	for {
		select {
		case <-ctx.Done():
			return targets.Result{ExitCode: -1}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		var inv struct {
			Status       string `json:"Status"`
			ResponseCode int    `json:"ResponseCode"`
			Stdout       string `json:"StandardOutputContent"`
			Stderr       string `json:"StandardErrorContent"`
			Detail       string `json:"StatusDetails"`
		}
		err := c.jsonCall(ctx, "ssm", "AmazonSSM.GetCommandInvocation", map[string]any{"CommandId": sent.Command.CommandID, "InstanceId": instanceID}, &inv)
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) && ae.Code == "InvocationDoesNotExist" {
				continue
			}
			return targets.Result{ExitCode: -1}, err
		}
		switch inv.Status {
		case "Pending", "InProgress", "Delayed":
			continue
		}
		col.Write("stdout", []byte(inv.Stdout))
		col.Write("stderr", []byte(inv.Stderr))
		col.Flush()
		res := targets.Result{ExitCode: inv.ResponseCode, Stdout: col.Stdout(), Stderr: col.Stderr()}
		switch inv.Status {
		case "Success":
			res.ExitCode = 0
			return res, nil
		case "Failed":
			if res.ExitCode == 0 {
				res.ExitCode = 1
			}
			return res, nil
		default:
			if res.ExitCode == 0 {
				res.ExitCode = -1
			}
			return res, fmt.Errorf("ssm command %s: %s", inv.Status, inv.Detail)
		}
	}
}

// CopyTo writes a file on the instance through SSM.
func (c *Client) CopyTo(ctx context.Context, spec targets.RunSpec, dest string, content []byte, mode int64) error {
	if mode == 0 {
		mode = 0o644
	}
	enc := base64.StdEncoding.EncodeToString(content)
	var sb strings.Builder
	fmt.Fprintf(&sb, "set -e\nmkdir -p %s\n: > %s\n", targets.ShellQuote(dirOf(dest)), targets.ShellQuote(dest))
	for off := 0; off < len(enc); off += ssmChunk {
		end := off + ssmChunk
		if end > len(enc) {
			end = len(enc)
		}
		fmt.Fprintf(&sb, "printf '%%s' '%s' | base64 -d >> %s\n", enc[off:end], targets.ShellQuote(dest))
	}
	fmt.Fprintf(&sb, "chmod %o %s\n", mode, targets.ShellQuote(dest))
	spec.Argv = nil
	spec.Script = sb.String()
	res, err := c.Run(ctx, spec, nil)
	if err != nil {
		return err
	}
	return res.Error()
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "/"
}
