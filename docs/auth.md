# Auth (bearer tokens)

localmind ships two HTTP services on the host network:

- the **MCP gateway** on `:7800` (`/mcp`) — anyone who can reach it can
  search and read every indexed file;
- the **responder** on `:7900` (`/wake`, `/status`, `/`) — anyone who can
  reach it can spin up your docker stack on demand.

By default both run unauthenticated. That's fine on a single-user laptop,
or behind a tailnet you trust. It is **not** fine on a corporate LAN, a
coffee-shop wifi, or anywhere the listening port might be reachable by
something you don't control.

This page describes the optional bearer-token mode that turns both
services into "you need the secret to talk to me" endpoints.

## Threat model — what this does and doesn't do

This is **defense-in-depth**, not real authentication. Specifically:

- The token is a single shared static string. There is no rotation, no
  revocation, no per-client identity, no audit log of who used it.
- It lives in plain text in your `.env` file, in the responder's URL bar
  (which means browser history and the Tailscale Funnel access log), and
  in whatever tool you paste it into.
- We do constant-time compare so you don't leak it via timing, but that's
  the floor of the security model, not the ceiling.

Treat the token like a Plex Pin: it raises the bar from "anyone on the
network can dump my files" to "you need a non-trivial secret first." If
you actually need real auth (multi-user, revocable, auditable), this
project would have to grow OAuth — that's not on the v0 roadmap.

The intended deployment is: tailnet first, token second. The token is
what stops a Tailscale device that *isn't yours* (a borrowed laptop, an
unaudited share-node) from talking to your stack just because it happens
to be on the same tailnet.

## Enabling it

Generate a token (any reasonably long random string is fine):

```sh
openssl rand -hex 32
```

Set the same value (or two different values, your choice) in your
`.env`:

```ini
# .env
LOCALMIND_RESPONDER_TOKEN=<paste-from-openssl>
LOCALMIND_MCP_TOKEN=<paste-from-openssl>
```

Restart the responder and the docker stack so they pick up the new env:

```sh
localmind responder uninstall && localmind responder install
localmind down && localmind up
```

When the env var is **unset or empty**, that service runs as it always
has — no auth at all. This is intentional so existing single-user
installs don't break on upgrade.

`/healthz` stays open on both services regardless. Monitoring needs it,
and a 200 leaks no information beyond "the process is alive."

## Using the token

### From Claude Code (MCP gateway)

When registering the gateway with `claude mcp add`, attach an
`Authorization` header:

```sh
claude mcp add localmind http://localhost:7800/mcp \
  --header "Authorization: Bearer <your-token>"
```

If your version of the CLI doesn't have a `--header` flag, edit the
generated `mcp.json` directly and add a `headers` object:

```json
{
  "mcpServers": {
    "localmind": {
      "url": "http://localhost:7800/mcp",
      "headers": { "Authorization": "Bearer <your-token>" }
    }
  }
}
```

### From your phone (responder HTML page)

Browsers can't easily inject `Authorization` headers when you type a URL
or open a bookmark, so the responder also accepts the token as a query
parameter. Bookmark the Tailscale Funnel URL with the token appended:

```
https://localmind.<tailnet>.ts.net/?token=<your-token>
```

The embedded HTML page reads `?token=` from `location.search` and
forwards it to its own `/status` and `/wake` calls. When the page
finishes waking the stack and redirects to Open WebUI, the token is
**not** carried along — Open WebUI has its own login.

### From `curl` / scripts

Either form works:

```sh
curl -H "Authorization: Bearer $TOKEN" http://localhost:7900/status
curl "http://localhost:7900/status?token=$TOKEN"
```

Wrong-or-missing token returns plain `401 unauthorized`.

## What's not protected

- `/healthz` on both services — by design.
- Open WebUI itself — it has its own auth and is not in scope here.
- Ollama (`:11434`), Whisper, Piper — these listen on the docker
  network, not the host. If you've published their ports to the host
  for some reason, you'll need to firewall them yourself; this token
  doesn't reach them.
