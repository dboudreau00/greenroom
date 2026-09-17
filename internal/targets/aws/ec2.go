package aws

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls AWS services in one region with one credential provider.
type Client struct {
	Region   string
	Provider *Provider
	http     *http.Client
	// Endpoint overrides (tests): service name -> URL.
	Endpoints map[string]string
}

// NewClient builds a client. An empty region is resolved through the
// environment and shared config.
func NewClient(region, profile string) (*Client, error) {
	region = ResolveRegion(region, profile)
	if region == "" {
		return nil, errors.New("aws target needs a region (set target.region or AWS_REGION)")
	}
	hc := &http.Client{Timeout: 60 * time.Second}
	return &Client{Region: region, Provider: NewProvider(profile, region, hc), http: hc, Endpoints: map[string]string{}}, nil
}

func (c *Client) endpoint(service string) string {
	if e, ok := c.Endpoints[service]; ok {
		return e
	}
	return "https://" + service + "." + c.Region + ".amazonaws.com/"
}

// APIError is an error response from AWS.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("aws: %d %s: %s", e.Status, e.Code, e.Message) }

func parseXMLError(status int, b []byte) error {
	var v struct {
		Errors []struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Errors>Error"`
		Error struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
	}
	xml.Unmarshal(b, &v)
	ae := &APIError{Status: status, Code: v.Error.Code, Message: v.Error.Message}
	if len(v.Errors) > 0 {
		ae.Code, ae.Message = v.Errors[0].Code, v.Errors[0].Message
	}
	if ae.Message == "" {
		ae.Message = strings.TrimSpace(string(b))
		if len(ae.Message) > 300 {
			ae.Message = ae.Message[:300]
		}
	}
	return ae
}

// query performs an EC2/STS style query-protocol call.
func (c *Client) query(ctx context.Context, service, action, version string, params map[string]string, out any) error {
	creds, err := c.Provider.Get(ctx)
	if err != nil {
		return err
	}
	all := map[string]string{"Action": action, "Version": version}
	for k, v := range params {
		all[k] = v
	}
	body := FormEncode(all)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(service), strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	Sign(req, creds, c.Region, service, []byte(body), time.Now())
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", service, action, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return parseXMLError(resp.StatusCode, b)
	}
	if out == nil {
		return nil
	}
	if err := xml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s %s: bad response: %w", service, action, err)
	}
	return nil
}

// Instance is a normalised EC2 instance.
type Instance struct {
	ID             string    `json:"id"`
	Name           string    `json:"name,omitempty"`
	State          string    `json:"state"`
	Type           string    `json:"type"`
	ImageID        string    `json:"imageId"`
	PrivateIP      string    `json:"privateIp,omitempty"`
	PublicIP       string    `json:"publicIp,omitempty"`
	LaunchTime     time.Time `json:"launchTime"`
	SystemStatus   string    `json:"systemStatus,omitempty"`
	InstanceStatus string    `json:"instanceStatus,omitempty"`
	AZ             string    `json:"az,omitempty"`
}

// Healthy reports running with passing status checks (when reported).
func (i Instance) Healthy() bool {
	if i.State != "running" {
		return false
	}
	okStatus := func(s string) bool { return s == "" || s == "ok" || s == "initializing" || s == "not-applicable" }
	return okStatus(i.SystemStatus) && okStatus(i.InstanceStatus)
}

const ec2Version = "2016-11-15"

// DescribeInstances returns the named instances.
func (c *Client) DescribeInstances(ctx context.Context, ids ...string) ([]Instance, error) {
	params := map[string]string{}
	for i, id := range ids {
		params[fmt.Sprintf("InstanceId.%d", i+1)] = id
	}
	var out struct {
		Reservations []struct {
			Instances []struct {
				ID         string `xml:"instanceId"`
				Type       string `xml:"instanceType"`
				ImageID    string `xml:"imageId"`
				PrivateIP  string `xml:"privateIpAddress"`
				PublicIP   string `xml:"ipAddress"`
				LaunchTime string `xml:"launchTime"`
				State      struct {
					Name string `xml:"name"`
				} `xml:"instanceState"`
				Placement struct {
					AZ string `xml:"availabilityZone"`
				} `xml:"placement"`
				Tags []struct {
					Key   string `xml:"key"`
					Value string `xml:"value"`
				} `xml:"tagSet>item"`
			} `xml:"instancesSet>item"`
		} `xml:"reservationSet>item"`
	}
	if err := c.query(ctx, "ec2", "DescribeInstances", ec2Version, params, &out); err != nil {
		return nil, err
	}
	var list []Instance
	for _, r := range out.Reservations {
		for _, in := range r.Instances {
			inst := Instance{ID: in.ID, Type: in.Type, ImageID: in.ImageID, PrivateIP: in.PrivateIP, PublicIP: in.PublicIP, State: in.State.Name, AZ: in.Placement.AZ}
			inst.LaunchTime, _ = time.Parse(time.RFC3339, in.LaunchTime)
			for _, t := range in.Tags {
				if t.Key == "Name" {
					inst.Name = t.Value
				}
			}
			list = append(list, inst)
		}
	}
	return list, nil
}

// DescribeInstanceStatus fills in system and instance status checks.
func (c *Client) DescribeInstanceStatus(ctx context.Context, ids ...string) (map[string][2]string, error) {
	params := map[string]string{"IncludeAllInstances": "true"}
	for i, id := range ids {
		params[fmt.Sprintf("InstanceId.%d", i+1)] = id
	}
	var out struct {
		Items []struct {
			ID     string `xml:"instanceId"`
			System struct {
				Status string `xml:"status"`
			} `xml:"systemStatus"`
			Instance struct {
				Status string `xml:"status"`
			} `xml:"instanceStatus"`
		} `xml:"instanceStatusSet>item"`
	}
	if err := c.query(ctx, "ec2", "DescribeInstanceStatus", ec2Version, params, &out); err != nil {
		return nil, err
	}
	res := map[string][2]string{}
	for _, it := range out.Items {
		res[it.ID] = [2]string{it.System.Status, it.Instance.Status}
	}
	return res, nil
}

// Instance describes one instance including status checks.
func (c *Client) Instance(ctx context.Context, id string) (*Instance, error) {
	list, err := c.DescribeInstances(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("instance %s not found", id)
	}
	inst := list[0]
	if st, err := c.DescribeInstanceStatus(ctx, id); err == nil {
		if s, ok := st[id]; ok {
			inst.SystemStatus, inst.InstanceStatus = s[0], s[1]
		}
	}
	return &inst, nil
}

// StartInstances starts stopped instances.
func (c *Client) StartInstances(ctx context.Context, ids ...string) error {
	params := map[string]string{}
	for i, id := range ids {
		params[fmt.Sprintf("InstanceId.%d", i+1)] = id
	}
	return c.query(ctx, "ec2", "StartInstances", ec2Version, params, nil)
}

// StopInstances stops instances.
func (c *Client) StopInstances(ctx context.Context, ids ...string) error {
	params := map[string]string{}
	for i, id := range ids {
		params[fmt.Sprintf("InstanceId.%d", i+1)] = id
	}
	return c.query(ctx, "ec2", "StopInstances", ec2Version, params, nil)
}

// RebootInstances reboots instances.
func (c *Client) RebootInstances(ctx context.Context, ids ...string) error {
	params := map[string]string{}
	for i, id := range ids {
		params[fmt.Sprintf("InstanceId.%d", i+1)] = id
	}
	return c.query(ctx, "ec2", "RebootInstances", ec2Version, params, nil)
}

// WaitInstance polls until the instance reaches state (running or stopped).
func (c *Client) WaitInstance(ctx context.Context, id, state string, timeout time.Duration) (*Instance, error) {
	deadline := time.Now().Add(timeout)
	for {
		inst, err := c.Instance(ctx, id)
		if err != nil {
			return nil, err
		}
		if inst.State == state && (state != "running" || inst.Healthy()) {
			return inst, nil
		}
		if time.Now().After(deadline) {
			return inst, fmt.Errorf("instance %s is %s after %s (wanted %s)", id, inst.State, timeout, state)
		}
		select {
		case <-ctx.Done():
			return inst, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// Ping verifies credentials and region by calling STS GetCallerIdentity.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var out struct {
		Result struct {
			Account string `xml:"Account"`
			Arn     string `xml:"Arn"`
		} `xml:"GetCallerIdentityResult"`
	}
	if err := c.query(ctx, "sts", "GetCallerIdentity", "2011-06-15", nil, &out); err != nil {
		return "", err
	}
	creds, _ := c.Provider.Get(ctx)
	return fmt.Sprintf("aws account %s as %s in %s (%s)", out.Result.Account, out.Result.Arn, c.Region, creds.Source), nil
}
