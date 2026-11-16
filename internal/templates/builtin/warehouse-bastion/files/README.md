# {{title}}

Created from the `{{template}}` template with these modules: {{modules}}.

The warehouse at `{{warehouseHost}}` is only reachable from the bastion
`{{instanceId}}` in `{{region}}`. Greenroom runs psql on the bastion through
SSM Run Command, so the instance needs the SSM agent, an instance profile
with `AmazonSSMManagedInstanceCore`, and `psql` installed.

```bash
greenroom targets {{env}}
greenroom check {{env}}
greenroom reset {{env}}
```

The bastion is started automatically when it is found stopped
(`autoHeal`). The BI app at `{{appUrl}}` and its certificate are probed on
every check.
