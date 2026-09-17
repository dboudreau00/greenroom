# Greenroom on EC2

Greenroom is one static binary. On an instance it runs as a systemd service
and reaches other instances through the EC2 and SSM APIs using the instance
profile, so no access keys are stored anywhere.

## Install

1. Create an IAM role for the portal instance with `iam-policy.json`
   attached. The policy only allows start, stop, reboot and SSM Run Command
   on instances tagged `greenroom=managed`; tag the demo instances (the SQL
   bastion, the app servers) accordingly.
2. Make sure the managed instances run the SSM agent and have an instance
   profile with `AmazonSSMManagedInstanceCore`, so `ssm:SendCommand` can
   reach them.
3. Launch the portal instance with `user-data.sh` as user data (set
   `GREENROOM_TOKEN` in the script or let it generate one; it lands in
   `/etc/greenroom/env`). Open port 8470 in the security group to the
   people who run demos, or put it behind a load balancer with TLS.
4. Copy your workspace to `/opt/greenroom` (`greenroom.json` plus
   `environments/`) and `systemctl restart greenroom`.

## Environment example

```json
"targets": {
  "aws": { "kind": "aws", "region": "us-east-1" }
},
"dependencies": [
  {
    "name": "sql-bastion",
    "title": "SQL bastion",
    "kind": "ec2",
    "target": "aws",
    "instanceId": "i-0123456789abcdef0",
    "critical": true,
    "heal": { "action": "start" },
    "autoHeal": true
  },
  {
    "name": "warehouse",
    "kind": "postgres",
    "critical": true,
    "via": { "target": "aws", "instanceId": "i-0123456789abcdef0", "tool": "psql", "dsn": "postgres://demo@10.0.1.20/demo" },
    "reset": { "strategy": "schema" },
    "drift": [ { "name": "orders", "query": "select count(*) from orders" } ]
  }
]
```

The `via` block runs `psql` on the bastion through SSM Run Command, so the
warehouse never has to be reachable from the portal itself. The bastion's
uptime is sampled every probe interval, an open incident shows on the
dependency card, and with `autoHeal` a stopped bastion is started within
one interval plus the heal cooldown.

## Updating

```bash
sudo systemctl stop greenroom
sudo curl -fsSL https://github.com/dboudreau00/greenroom/releases/latest/download/greenroom-linux-amd64 -o /usr/local/bin/greenroom
sudo systemctl start greenroom
```
